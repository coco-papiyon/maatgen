// Package githuboutbox turns GitHub monitor jobs (events with status "queued" or
// "session_created") into ordinary Agent Sessions and Runs (ADR-007 sections
// 4 and 6): it is the "separate dispatcher" the ADR requires to read the
// Outbox jobs (called events in the protocol, but treated as jobs to be
// executed) that internal/githubmonitor's Evaluator writes and advance them
// through session_created -> run_started -> a terminal status, while
// respecting each repository's execution lock, each rule's concurrency
// policy, and provider quota availability (prioritizing jobs for providers
// with available quota).
package githuboutbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubmonitor"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
)

// defaultCoalesceQueueLimit backstops GitHubRepositoryMonitor.CoalesceQueueLimit
// for rows that predate the column or were left at zero.
const defaultCoalesceQueueLimit = 20

// batchSize caps how many events of a given status Tick considers per
// pass, bounding worst-case work per tick on a repository with many
// pending events.
const batchSize = 200

// Store is the persistence dependency Dispatcher needs. It is satisfied by
// *sqlite.Store.
type Store interface {
	GetTriggerRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error)
	GetRepositoryMonitor(ctx context.Context, repository string) (protocol.GitHubRepositoryMonitor, error)
	ListMonitorEventsByStatus(ctx context.Context, status protocol.GitHubMonitorEventStatus, limit int) ([]protocol.GitHubMonitorEvent, error)
	AttachMonitorEventSession(ctx context.Context, id, sessionID string, updatedAt time.Time) error
	AttachMonitorEventRun(ctx context.Context, id, runID string, updatedAt time.Time) error
	UpdateMonitorEventStatus(ctx context.Context, id string, status protocol.GitHubMonitorEventStatus, updatedAt time.Time) error
	SkipMonitorEvent(ctx context.Context, id, reason string, updatedAt time.Time) error
	FailMonitorEvent(ctx context.Context, id, lastError string, updatedAt time.Time) error
	GetRun(ctx context.Context, id string) (protocol.AgentRun, error)
}

// SessionCreator is satisfied by *session.Service. The dispatcher never
// creates more than one Session per Outbox event (ADR-007 section 4:
// automated Runs must not be mixed into an existing manual Session).
type SessionCreator interface {
	CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error)
}

// RunStarter is satisfied by *run.Service.
type RunStarter interface {
	StartRun(ctx context.Context, sessionID string, request protocol.SendMessageRequest) (protocol.AgentRun, error)
	CancelRun(ctx context.Context, runID string) error
	IsRepositoryBusy(repository string) bool
}

// ProviderUsageReader is satisfied by *providerusage.Service. It is optional;
// if nil, provider usage checks are skipped.
type ProviderUsageReader interface {
	GetProviderUsage(ctx context.Context, provider protocol.AgentName, directory string) (protocol.ProviderUsage, error)
}

// Dispatcher is the Outbox dispatcher. Register ObserveRunTerminal with the
// run.Service the RunStarter wraps (run.WithTerminalObserver) so it learns
// when a Run it started finishes.
type Dispatcher struct {
	store         Store
	sessions      SessionCreator
	runs          RunStarter
	providerUsage ProviderUsageReader // optional; if nil, usage checks are skipped
	now           func() time.Time
	pollInterval  time.Duration

	mu      sync.Mutex
	tracked map[string]string // runID -> monitor event ID

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type Option func(*Dispatcher)

// WithPollInterval overrides how often Start's background loop calls Tick.
func WithPollInterval(interval time.Duration) Option {
	return func(d *Dispatcher) { d.pollInterval = interval }
}

// WithProviderUsage sets the provider usage reader for checking quota before runs.
func WithProviderUsage(reader ProviderUsageReader) Option {
	return func(d *Dispatcher) { d.providerUsage = reader }
}

func NewDispatcher(store Store, sessions SessionCreator, runs RunStarter, options ...Option) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		store: store, sessions: sessions, runs: runs,
		now: time.Now, pollInterval: 5 * time.Second,
		tracked: make(map[string]string),
		ctx:     ctx, cancel: cancel,
	}
	for _, option := range options {
		option(d)
	}
	return d
}

// Start launches the background polling loop. Call Close to stop it.
func (d *Dispatcher) Start() {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(d.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-d.ctx.Done():
				return
			case <-ticker.C:
				if err := d.Tick(d.ctx); err != nil {
					slog.Error("github monitor outbox tick failed", "error", err)
				}
			}
		}
	}()
}

// Close stops the background loop and waits for the in-flight tick, if any,
// to finish.
func (d *Dispatcher) Close(ctx context.Context) error {
	d.cancel()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sortJobsByProviderAvailability reorders jobs so that jobs for providers
// with available quota are processed before jobs whose provider quota is exhausted.
// Jobs for the same exhausted provider remain queued for when quota recovers.
func (d *Dispatcher) sortJobsByProviderAvailability(ctx context.Context, jobs []protocol.GitHubMonitorEvent) []protocol.GitHubMonitorEvent {
	if d.providerUsage == nil {
		return jobs // No sorting if usage reader unavailable
	}
	// Collect provider availability: provider name -> hasQuota
	providerQuota := make(map[protocol.AgentName]bool)

	type jobWithProvider struct {
		event    protocol.GitHubMonitorEvent
		provider protocol.AgentName
		hasQuota bool
	}
	var jobsWithProviders []jobWithProvider

	for _, job := range jobs {
		rule, err := d.store.GetTriggerRule(ctx, *job.RuleID)
		if err != nil {
			// On error, keep job in original position
			jobsWithProviders = append(jobsWithProviders, jobWithProvider{event: job, provider: "", hasQuota: true})
			continue
		}
		provider := rule.Provider

		// Check quota for this provider (cache results)
		quota, ok := providerQuota[provider]
		if !ok {
			canProceed, _, _ := d.checkProviderUsageReady(ctx, provider, job.Repository)
			quota = canProceed
			providerQuota[provider] = quota
		}
		jobsWithProviders = append(jobsWithProviders, jobWithProvider{event: job, provider: provider, hasQuota: quota})
	}

	// Sort: jobs with quota first, then jobs without
	sort.SliceStable(jobsWithProviders, func(i, j int) bool {
		return jobsWithProviders[i].hasQuota && !jobsWithProviders[j].hasQuota
	})

	sorted := make([]protocol.GitHubMonitorEvent, len(jobsWithProviders))
	for i, j := range jobsWithProviders {
		sorted[i] = j.event
	}
	return sorted
}

// jobPriorityRank orders protocol.GitHubJobPriority values from most to
// least urgent. An unrecognized or empty priority (e.g. a job whose rule was
// deleted) ranks as medium, matching normalizeJobPriority's default.
var jobPriorityRank = map[protocol.GitHubJobPriority]int{
	protocol.GitHubPriorityHigh:   0,
	protocol.GitHubPriorityMedium: 1,
	protocol.GitHubPriorityLow:    2,
}

// sortJobsByPriority reorders jobs so that jobs matched by a higher-priority
// rule are processed first (issue #13). It is a stable sort, so jobs with
// the same priority keep the FIFO order Tick received them in.
func (d *Dispatcher) sortJobsByPriority(ctx context.Context, jobs []protocol.GitHubMonitorEvent) []protocol.GitHubMonitorEvent {
	type jobWithPriority struct {
		event protocol.GitHubMonitorEvent
		rank  int
	}
	jobsWithPriority := make([]jobWithPriority, len(jobs))
	for i, job := range jobs {
		rank := jobPriorityRank[protocol.GitHubPriorityMedium]
		if job.RuleID != nil {
			if rule, err := d.store.GetTriggerRule(ctx, *job.RuleID); err == nil {
				if r, ok := jobPriorityRank[rule.Priority]; ok {
					rank = r
				}
			}
		}
		jobsWithPriority[i] = jobWithPriority{event: job, rank: rank}
	}

	sort.SliceStable(jobsWithPriority, func(i, j int) bool {
		return jobsWithPriority[i].rank < jobsWithPriority[j].rank
	})

	sorted := make([]protocol.GitHubMonitorEvent, len(jobsWithPriority))
	for i, j := range jobsWithPriority {
		sorted[i] = j.event
	}
	return sorted
}

// Tick processes one batch of pending Outbox jobs: it advances "queued"
// jobs (create Session, start Run) and "session_created" jobs (a
// Session exists from a previous, interrupted attempt; start its Run), in
// each case honoring the matched rule's concurrency policy when the
// repository's execution lock is held by another Run.
// Jobs are ordered by the matched rule's priority (high before medium before
// low, ties broken by detection order), then by provider availability: if a
// provider's quota is exhausted, jobs for other providers in the queue are
// processed first.
func (d *Dispatcher) Tick(ctx context.Context) error {
	queued, err := d.store.ListMonitorEventsByStatus(ctx, protocol.GitHubMonitorEventQueued, batchSize)
	if err != nil {
		return fmt.Errorf("list queued github monitor jobs: %w", err)
	}
	queued = d.reconcileCoalesceQueue(ctx, queued)
	// Sort jobs by rule priority first (stable, so detection order breaks ties),
	// then by provider availability (stable, so priority order is preserved
	// within each availability group).
	queued = d.sortJobsByPriority(ctx, queued)
	queued = d.sortJobsByProviderAvailability(ctx, queued)
	for _, event := range queued {
		d.dispatchQueued(ctx, event)
	}

	sessionCreated, err := d.store.ListMonitorEventsByStatus(ctx, protocol.GitHubMonitorEventSessionCreated, batchSize)
	if err != nil {
		return fmt.Errorf("list session_created github monitor jobs: %w", err)
	}
	for _, event := range sessionCreated {
		d.dispatchSessionCreated(ctx, event)
	}
	return nil
}

// Reconcile recovers from a Manager restart (ADR-007 section 6): a "queued"
// or "session_created" job is safely retried by Tick's normal flow (it
// never got far enough to start a Run), but a "run_started" job's Run may
// have already reached a terminal state before the process exited, with no
// ObserveRunTerminal call ever delivered for it. Reconcile catches those up
// by reading the Run's current status directly. Call it once at startup,
// after the Run store's own interrupted-run recovery.
func (d *Dispatcher) Reconcile(ctx context.Context) error {
	events, err := d.store.ListMonitorEventsByStatus(ctx, protocol.GitHubMonitorEventRunStarted, batchSize)
	if err != nil {
		return fmt.Errorf("list run_started github monitor jobs: %w", err)
	}
	for _, event := range events {
		if event.RunID == nil {
			d.failEvent(ctx, event.ID, "run_started job is missing its run id")
			continue
		}
		runRecord, err := d.store.GetRun(ctx, *event.RunID)
		if err != nil {
			d.failEvent(ctx, event.ID, "associated run could not be found: "+err.Error())
			continue
		}
		if isTerminalRunStatus(runRecord.Status) {
			d.finishTrackedEvent(ctx, event.ID, runRecord.Status)
			continue
		}
		// Still active: the process must have exited before FailInterruptedRuns
		// ran, or this call raced with it. Track it so ObserveRunTerminal
		// catches the eventual transition instead of leaving it stale forever.
		d.track(*event.RunID, event.ID)
	}
	return nil
}

// ObserveRunTerminal is a run.WithTerminalObserver callback. It is called
// for every Run in the system; it only acts on ones this dispatcher started.
func (d *Dispatcher) ObserveRunTerminal(run protocol.AgentRun) {
	d.mu.Lock()
	eventID, tracked := d.tracked[run.ID]
	if tracked {
		delete(d.tracked, run.ID)
	}
	d.mu.Unlock()
	if !tracked {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d.finishTrackedEvent(ctx, eventID, run.Status)
}

func (d *Dispatcher) finishTrackedEvent(ctx context.Context, eventID string, status protocol.RunStatus) {
	now := d.now().UTC()
	switch status {
	case protocol.RunCompleted:
		if err := d.store.UpdateMonitorEventStatus(ctx, eventID, protocol.GitHubMonitorEventCompleted, now); err != nil {
			slog.Error("mark github monitor event completed failed", "event", eventID, "error", err)
		}
	case protocol.RunCancelled:
		if err := d.store.UpdateMonitorEventStatus(ctx, eventID, protocol.GitHubMonitorEventCancelled, now); err != nil {
			slog.Error("mark github monitor event cancelled failed", "event", eventID, "error", err)
		}
	default:
		d.failEvent(ctx, eventID, "automated run failed")
	}
}

func (d *Dispatcher) dispatchQueued(ctx context.Context, event protocol.GitHubMonitorEvent) {
	rule, ok := d.requireRule(ctx, event)
	if !ok {
		return
	}
	monitor, err := d.store.GetRepositoryMonitor(ctx, event.Repository)
	if err != nil {
		d.failEvent(ctx, event.ID, "repository monitor could not be found: "+err.Error())
		return
	}
	prompt, err := renderPrompt(rule, monitor, event)
	if err != nil {
		d.failEvent(ctx, event.ID, err.Error())
		return
	}
	if d.runs.IsRepositoryBusy(event.Repository) {
		if rule.ConcurrencyPolicy == protocol.GitHubConcurrencySkip {
			d.skipEvent(ctx, event.ID, "repository execution lock is held by another run")
		}
		// coalesce: leave the event queued for the next Tick. No Session is
		// created until the repository can actually start processing it.
		return
	}

	req := protocol.CreateSessionRequest{
		Agent:              rule.Provider,
		Workspace:          event.Repository,
		TriggerSource:      protocol.TriggerSourceGitHubMonitor,
		GitHubMonitorEvent: &event.ID,
		GitHubRuleID:       event.RuleID,
		GitHubItemKind:     &event.Kind,
		GitHubItemNumber:   &event.Number,
	}
	session, err := d.sessions.CreateSession(ctx, req)
	if err != nil {
		d.failEvent(ctx, event.ID, "create session failed: "+err.Error())
		return
	}
	if err := d.store.AttachMonitorEventSession(ctx, event.ID, session.ID, d.now().UTC()); err != nil {
		d.failEvent(ctx, event.ID, "attach session failed: "+err.Error())
		return
	}
	event.SessionID = &session.ID

	d.startRun(ctx, event, rule, session.ID, session.Workspace, session.Agent, prompt)
}

// checkProviderUsageReady checks if the provider has available quota.
// Returns (canProceed, waitDuration, error).
// If providerUsage reader is nil, assumes quota is available.
func (d *Dispatcher) checkProviderUsageReady(ctx context.Context, provider protocol.AgentName, directory string) (bool, time.Duration, error) {
	if d.providerUsage == nil {
		return true, 0, nil
	}
	usage, err := d.providerUsage.GetProviderUsage(ctx, provider, directory)
	if err != nil {
		// Fetch failure does not block execution (provider usage monitoring is optional).
		return true, 0, nil
	}
	for _, window := range usage.Windows {
		if window.RemainingPercent <= 0 {
			// Remaining quota is exhausted. Estimate wait time from reset label if available.
			waitDuration := 5 * time.Minute // default fallback
			if window.ResetLabel != "" {
				if resetTime, err := time.Parse(time.RFC3339, window.ResetLabel); err == nil {
					if until := time.Until(resetTime); until > 0 {
						waitDuration = until + 30*time.Second // small buffer after reset
					}
				}
			}
			return false, waitDuration, nil
		}
	}
	return true, 0, nil
}

func (d *Dispatcher) dispatchSessionCreated(ctx context.Context, event protocol.GitHubMonitorEvent) {
	if event.SessionID == nil {
		d.failEvent(ctx, event.ID, "session_created event is missing its session id")
		return
	}
	rule, ok := d.requireRule(ctx, event)
	if !ok {
		return
	}
	monitor, err := d.store.GetRepositoryMonitor(ctx, event.Repository)
	if err != nil {
		d.failEvent(ctx, event.ID, "repository monitor could not be found: "+err.Error())
		return
	}
	prompt, err := renderPrompt(rule, monitor, event)
	if err != nil {
		d.failEvent(ctx, event.ID, err.Error())
		return
	}
	d.startRun(ctx, event, rule, *event.SessionID, event.Repository, rule.Provider, prompt)
}

func (d *Dispatcher) startRun(ctx context.Context, event protocol.GitHubMonitorEvent, rule protocol.GitHubTriggerRule, sessionID, workspace string, provider protocol.AgentName, prompt string) {
	// Check provider usage quota before starting the run.
	canProceed, waitDuration, _ := d.checkProviderUsageReady(ctx, provider, workspace)
	if !canProceed {
		if rule.ConcurrencyPolicy == protocol.GitHubConcurrencySkip {
			d.skipEvent(ctx, event.ID, fmt.Sprintf("provider quota exhausted, will retry after %v", waitDuration))
		}
		// coalesce: leave the event queued for the next Tick so it retries when quota recovers.
		return
	}

	startedRun, err := d.runs.StartRun(ctx, sessionID, protocol.SendMessageRequest{
		Message: prompt, Model: rule.Model, ReasoningEffort: rule.ReasoningEffort,
	})
	if err != nil {
		if errors.Is(err, runservice.ErrRepositoryBusy) {
			if rule.ConcurrencyPolicy == protocol.GitHubConcurrencySkip {
				d.skipEvent(ctx, event.ID, "repository execution lock is held by another run")
			}
			// coalesce: leave the event as-is; it is retried on the next Tick.
			return
		}
		d.failEvent(ctx, event.ID, "start run failed: "+err.Error())
		return
	}
	if err := d.store.AttachMonitorEventRun(ctx, event.ID, startedRun.ID, d.now().UTC()); err != nil {
		// The Run has already been started (and may be mutating the Working
		// Tree) but its association with this event could not be persisted.
		// Cancel it so it does not keep running unassociated with any
		// monitor event; the event itself is recorded failed, with the
		// orphaned Run's ID kept in the error message for manual recovery.
		if cancelErr := d.runs.CancelRun(ctx, startedRun.ID); cancelErr != nil {
			slog.Error("cancel orphaned run after attach failure failed", "event", event.ID, "run", startedRun.ID, "error", cancelErr)
		}
		slog.Error("attach github monitor event run failed", "event", event.ID, "run", startedRun.ID, "error", err)
		d.failEvent(ctx, event.ID, fmt.Sprintf("attach run failed (run %s was cancelled): %v", startedRun.ID, err))
		return
	}
	d.track(startedRun.ID, event.ID)
}

func (d *Dispatcher) requireRule(ctx context.Context, event protocol.GitHubMonitorEvent) (protocol.GitHubTriggerRule, bool) {
	if event.RuleID == nil {
		d.failEvent(ctx, event.ID, "event is missing its trigger rule id")
		return protocol.GitHubTriggerRule{}, false
	}
	rule, err := d.store.GetTriggerRule(ctx, *event.RuleID)
	if err != nil {
		d.failEvent(ctx, event.ID, "trigger rule could not be found: "+err.Error())
		return protocol.GitHubTriggerRule{}, false
	}
	return rule, true
}

func renderPrompt(rule protocol.GitHubTriggerRule, monitor protocol.GitHubRepositoryMonitor, event protocol.GitHubMonitorEvent) (string, error) {
	fields := githubmonitor.BuildPromptFields(monitor, event.ItemSnapshot, event.Action, rule.IncludeBody)
	prompt, err := githubmonitor.RenderPrompt(rule.PromptTemplate, fields)
	if err != nil {
		return "", fmt.Errorf("render prompt template: %w", err)
	}
	return prompt, nil
}

func (d *Dispatcher) skipEvent(ctx context.Context, eventID, reason string) {
	if err := d.store.SkipMonitorEvent(ctx, eventID, reason, d.now().UTC()); err != nil {
		slog.Error("skip github monitor event failed", "event", eventID, "error", err)
	}
}

func (d *Dispatcher) failEvent(ctx context.Context, eventID, reason string) {
	if err := d.store.FailMonitorEvent(ctx, eventID, reason, d.now().UTC()); err != nil {
		slog.Error("fail github monitor event failed", "event", eventID, "error", err)
	}
}

func (d *Dispatcher) track(runID, eventID string) {
	d.mu.Lock()
	d.tracked[runID] = eventID
	d.mu.Unlock()
}

func isTerminalRunStatus(status protocol.RunStatus) bool {
	return status == protocol.RunCompleted || status == protocol.RunFailed || status == protocol.RunCancelled
}

// coalesceKey identifies "the same item, under the same rule" for the
// coalescing behavior ADR-007 section 6 requires: repeated matches against
// one item while a rule's Runs are serialized should collapse to the
// latest, not queue up one Run per match.
type coalesceKey struct {
	repository string
	ruleID     string
	kind       protocol.GitHubItemKind
	number     int
}

// reconcileCoalesceQueue applies ADR-007 section 6's coalescing rules to a
// batch of "queued" events before dispatching any of them:
//
//   - Only events whose rule has ConcurrencyPolicy "coalesce" participate;
//     "skip" events are always dispatched (and, if the repository is busy,
//     skipped immediately by startRun) and never held.
//   - Within a (repository, rule, item kind, item number) group, only the
//     newest event survives; older ones are marked skipped as superseded.
//   - Each repository's number of distinct surviving groups is capped at
//     its configured CoalesceQueueLimit; the oldest groups beyond the limit
//     are marked skipped rather than silently dropped, so they remain
//     visible and replayable in the event history.
func (d *Dispatcher) reconcileCoalesceQueue(ctx context.Context, events []protocol.GitHubMonitorEvent) []protocol.GitHubMonitorEvent {
	groups := make(map[coalesceKey][]protocol.GitHubMonitorEvent)
	passthrough := make([]protocol.GitHubMonitorEvent, 0, len(events))

	for _, event := range events {
		if event.RuleID == nil {
			passthrough = append(passthrough, event)
			continue
		}
		rule, err := d.store.GetTriggerRule(ctx, *event.RuleID)
		if err != nil || rule.ConcurrencyPolicy != protocol.GitHubConcurrencyCoalesce {
			passthrough = append(passthrough, event)
			continue
		}
		key := coalesceKey{repository: event.Repository, ruleID: *event.RuleID, kind: event.Kind, number: event.Number}
		groups[key] = append(groups[key], event)
	}

	type survivor struct {
		key   coalesceKey
		event protocol.GitHubMonitorEvent
	}
	survivors := make([]survivor, 0, len(groups))
	for key, group := range groups {
		sort.Slice(group, func(i, j int) bool { return group[i].CreatedAt.Before(group[j].CreatedAt) })
		newest := group[len(group)-1]
		for _, stale := range group[:len(group)-1] {
			d.skipEvent(ctx, stale.ID, "superseded by a newer event for the same item")
		}
		survivors = append(survivors, survivor{key: key, event: newest})
	}

	byRepository := make(map[string][]survivor)
	for _, s := range survivors {
		byRepository[s.key.repository] = append(byRepository[s.key.repository], s)
	}

	result := passthrough
	for repository, group := range byRepository {
		limit := defaultCoalesceQueueLimit
		if monitor, err := d.store.GetRepositoryMonitor(ctx, repository); err == nil && monitor.CoalesceQueueLimit > 0 {
			limit = monitor.CoalesceQueueLimit
		}
		if len(group) <= limit {
			for _, s := range group {
				result = append(result, s.event)
			}
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].event.CreatedAt.Before(group[j].event.CreatedAt) })
		overflow := len(group) - limit
		for i, s := range group {
			if i < overflow {
				d.skipEvent(ctx, s.event.ID, "coalesce queue limit reached for this repository")
				continue
			}
			result = append(result, s.event)
		}
	}
	return result
}
