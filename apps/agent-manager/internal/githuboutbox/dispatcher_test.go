package githuboutbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

type fakeStore struct {
	mu       sync.Mutex
	rules    map[string]protocol.GitHubTriggerRule
	monitors map[string]protocol.GitHubRepositoryMonitor
	events   map[string]protocol.GitHubMonitorEvent
	runs     map[string]protocol.AgentRun
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rules:    map[string]protocol.GitHubTriggerRule{},
		monitors: map[string]protocol.GitHubRepositoryMonitor{},
		events:   map[string]protocol.GitHubMonitorEvent{},
		runs:     map[string]protocol.AgentRun{},
	}
}

func (f *fakeStore) addRule(rule protocol.GitHubTriggerRule) { f.rules[rule.ID] = rule }
func (f *fakeStore) addMonitor(monitor protocol.GitHubRepositoryMonitor) {
	f.monitors[monitor.Repository] = monitor
}
func (f *fakeStore) addEvent(event protocol.GitHubMonitorEvent)     { f.events[event.ID] = event }
func (f *fakeStore) addRun(run protocol.AgentRun)                   { f.runs[run.ID] = run }
func (f *fakeStore) getEvent(id string) protocol.GitHubMonitorEvent { return f.events[id] }

func (f *fakeStore) GetTriggerRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rule, ok := f.rules[id]
	if !ok {
		return protocol.GitHubTriggerRule{}, storage.ErrNotFound
	}
	return rule, nil
}

func (f *fakeStore) GetRepositoryMonitor(ctx context.Context, repository string) (protocol.GitHubRepositoryMonitor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	monitor, ok := f.monitors[repository]
	if !ok {
		return protocol.GitHubRepositoryMonitor{}, storage.ErrNotFound
	}
	return monitor, nil
}

func (f *fakeStore) ListMonitorEventsByStatus(ctx context.Context, status protocol.GitHubMonitorEventStatus, limit int) ([]protocol.GitHubMonitorEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []protocol.GitHubMonitorEvent
	for _, event := range f.events {
		if event.Status == status {
			result = append(result, event)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *fakeStore) AttachMonitorEventSession(ctx context.Context, id, sessionID string, updatedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	event, ok := f.events[id]
	if !ok {
		return storage.ErrNotFound
	}
	event.SessionID = &sessionID
	event.Status = protocol.GitHubMonitorEventSessionCreated
	event.UpdatedAt = updatedAt
	f.events[id] = event
	return nil
}

func (f *fakeStore) AttachMonitorEventRun(ctx context.Context, id, runID string, updatedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	event, ok := f.events[id]
	if !ok {
		return storage.ErrNotFound
	}
	event.RunID = &runID
	event.Status = protocol.GitHubMonitorEventRunStarted
	event.UpdatedAt = updatedAt
	f.events[id] = event
	return nil
}

func (f *fakeStore) UpdateMonitorEventStatus(ctx context.Context, id string, status protocol.GitHubMonitorEventStatus, updatedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	event, ok := f.events[id]
	if !ok {
		return storage.ErrNotFound
	}
	event.Status = status
	event.UpdatedAt = updatedAt
	f.events[id] = event
	return nil
}

func (f *fakeStore) SkipMonitorEvent(ctx context.Context, id, reason string, updatedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	event, ok := f.events[id]
	if !ok {
		return storage.ErrNotFound
	}
	event.Status = protocol.GitHubMonitorEventSkipped
	event.SkipReason = &reason
	event.UpdatedAt = updatedAt
	f.events[id] = event
	return nil
}

func (f *fakeStore) FailMonitorEvent(ctx context.Context, id, lastError string, updatedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	event, ok := f.events[id]
	if !ok {
		return storage.ErrNotFound
	}
	event.Status = protocol.GitHubMonitorEventFailed
	event.LastError = &lastError
	event.UpdatedAt = updatedAt
	f.events[id] = event
	return nil
}

func (f *fakeStore) GetRun(ctx context.Context, id string) (protocol.AgentRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[id]
	if !ok {
		return protocol.AgentRun{}, storage.ErrNotFound
	}
	return run, nil
}

type fakeSessions struct {
	mu      sync.Mutex
	created []protocol.CreateSessionRequest
	nextID  int
	err     error
}

func (f *fakeSessions) CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return protocol.AgentSession{}, f.err
	}
	f.created = append(f.created, request)
	f.nextID++
	return protocol.AgentSession{
		ID: fmt.Sprintf("session-%d", f.nextID), Agent: request.Agent, Workspace: request.Workspace,
		Status: protocol.SessionActive,
	}, nil
}

type runStartCall struct {
	sessionID string
	request   protocol.SendMessageRequest
}

type fakeRuns struct {
	mu        sync.Mutex
	calls     []runStartCall
	err       error
	nextID    int
	cancelled []string
	cancelErr error
	busy      bool
}

func (f *fakeRuns) StartRun(ctx context.Context, sessionID string, request protocol.SendMessageRequest) (protocol.AgentRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, runStartCall{sessionID: sessionID, request: request})
	if f.err != nil {
		return protocol.AgentRun{}, f.err
	}
	f.nextID++
	return protocol.AgentRun{ID: fmt.Sprintf("run-%d", f.nextID), SessionID: sessionID, Status: protocol.RunQueued}, nil
}

func (f *fakeRuns) CancelRun(ctx context.Context, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, runID)
	return f.cancelErr
}

func (f *fakeRuns) IsRepositoryBusy(repository string) bool { return f.busy }

func testMonitor(repository string) protocol.GitHubRepositoryMonitor {
	return protocol.GitHubRepositoryMonitor{
		Repository: repository, Host: "github.com", Owner: "octo-org", Name: "example",
		RemoteName: "origin", Enabled: true, PollIntervalSeconds: 300, CoalesceQueueLimit: 20,
	}
}

func testRule(id, repository string, policy protocol.GitHubConcurrencyPolicy) protocol.GitHubTriggerRule {
	return protocol.GitHubTriggerRule{
		ID: id, Repository: repository, Name: "rule", Enabled: true,
		EventKinds:        []protocol.GitHubItemKind{protocol.GitHubItemIssue},
		PromptTemplate:    "Design {{.Title}} (#{{.Number}})",
		Provider:          protocol.AgentCodex,
		ConcurrencyPolicy: policy,
	}
}

func testEvent(id, repository, ruleID string, status protocol.GitHubMonitorEventStatus, createdAt time.Time) protocol.GitHubMonitorEvent {
	return protocol.GitHubMonitorEvent{
		ID: id, Repository: repository, RuleID: &ruleID, Kind: protocol.GitHubItemIssue, Number: 1,
		Action: "opened", AfterStateHash: "hash", Status: status,
		ItemSnapshot: protocol.GitHubItem{Kind: protocol.GitHubItemIssue, Number: 1, Title: "Something"},
		CreatedAt:    createdAt, UpdatedAt: createdAt,
	}
}

func TestDispatchQueuedCreatesSessionAndStartsRun(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencyCoalesce))
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, time.Now())
	store.addEvent(event)

	sessions := &fakeSessions{}
	runs := &fakeRuns{}
	dispatcher := NewDispatcher(store, sessions, runs)

	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(sessions.created) != 1 || sessions.created[0].Workspace != "/repo" || sessions.created[0].Agent != protocol.AgentCodex {
		t.Fatalf("sessions.created = %#v", sessions.created)
	}
	if len(runs.calls) != 1 || runs.calls[0].sessionID != "session-1" {
		t.Fatalf("runs.calls = %#v", runs.calls)
	}
	if runs.calls[0].request.Message != "Design Something (#1)" {
		t.Fatalf("prompt = %q", runs.calls[0].request.Message)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventRunStarted {
		t.Fatalf("event.Status = %v, want run_started", got.Status)
	}
	if got.SessionID == nil || *got.SessionID != "session-1" || got.RunID == nil || *got.RunID != "run-1" {
		t.Fatalf("event = %#v", got)
	}
}

func TestDispatchQueuedFailsWhenRuleMissing(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	event := testEvent("event-1", "/repo", "missing-rule", protocol.GitHubMonitorEventQueued, time.Now())
	store.addEvent(event)

	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventFailed || got.LastError == nil {
		t.Fatalf("event = %#v", got)
	}
}

func TestDispatchSessionCreatedUsesExistingSession(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencyCoalesce))
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventSessionCreated, time.Now())
	sessionID := "session-existing"
	event.SessionID = &sessionID
	store.addEvent(event)

	sessions := &fakeSessions{}
	runs := &fakeRuns{}
	dispatcher := NewDispatcher(store, sessions, runs)
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(sessions.created) != 0 {
		t.Fatalf("a session_created event must not create another session: %#v", sessions.created)
	}
	if len(runs.calls) != 1 || runs.calls[0].sessionID != sessionID {
		t.Fatalf("runs.calls = %#v", runs.calls)
	}
}

func TestStartRunSkipPolicyMarksSkippedWhenRepositoryBusy(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencySkip))
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, time.Now())
	store.addEvent(event)

	runs := &fakeRuns{err: runservice.ErrRepositoryBusy, busy: true}
	dispatcher := NewDispatcher(store, &fakeSessions{}, runs)
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventSkipped {
		t.Fatalf("event.Status = %v, want skipped", got.Status)
	}
}

func TestStartRunCoalescePolicyLeavesEventForRetryWhenRepositoryBusy(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencyCoalesce))
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, time.Now())
	store.addEvent(event)

	runs := &fakeRuns{err: runservice.ErrRepositoryBusy, busy: true}
	dispatcher := NewDispatcher(store, &fakeSessions{}, runs)
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventQueued {
		t.Fatalf("event.Status = %v, want queued (no session made while repository is busy)", got.Status)
	}
	if got.SessionID != nil {
		t.Fatalf("SessionID = %#v, want nil while repository is busy", got.SessionID)
	}
	if got.RunID != nil {
		t.Fatalf("RunID = %#v, want nil: the run never actually started", got.RunID)
	}
}

func TestObserveRunTerminalIgnoresUntrackedRuns(t *testing.T) {
	store := newFakeStore()
	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	// Must not panic or touch the store for a run this dispatcher never started.
	dispatcher.ObserveRunTerminal(protocol.AgentRun{ID: "unrelated-run", Status: protocol.RunCompleted})
}

func TestObserveRunTerminalUpdatesTrackedEvent(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencyCoalesce))
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, time.Now())
	store.addEvent(event)

	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := store.getEvent("event-1")
	if got.RunID == nil {
		t.Fatalf("expected a run id to be attached")
	}

	dispatcher.ObserveRunTerminal(protocol.AgentRun{ID: *got.RunID, Status: protocol.RunCompleted})
	got = store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventCompleted {
		t.Fatalf("event.Status = %v, want completed", got.Status)
	}

	// A second terminal notification for the same run must be a no-op: the
	// event was already untracked after the first one.
	dispatcher.ObserveRunTerminal(protocol.AgentRun{ID: *got.RunID, Status: protocol.RunFailed})
	got = store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventCompleted {
		t.Fatalf("event.Status changed after being untracked: %v", got.Status)
	}
}

func TestReconcileMarksTerminalRunStartedEvents(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventRunStarted, time.Now())
	runID := "run-done"
	event.RunID = &runID
	store.addEvent(event)
	store.addRun(protocol.AgentRun{ID: runID, Status: protocol.RunCompleted})

	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventCompleted {
		t.Fatalf("event.Status = %v, want completed", got.Status)
	}
}

func TestReconcileTracksStillActiveRunForLaterObservation(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventRunStarted, time.Now())
	runID := "run-active"
	event.RunID = &runID
	store.addEvent(event)
	store.addRun(protocol.AgentRun{ID: runID, Status: protocol.RunRunning})

	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventRunStarted {
		t.Fatalf("event.Status = %v, want unchanged (still active)", got.Status)
	}

	dispatcher.ObserveRunTerminal(protocol.AgentRun{ID: runID, Status: protocol.RunCancelled})
	got = store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventCancelled {
		t.Fatalf("event.Status = %v, want cancelled after Reconcile re-tracked it", got.Status)
	}
}

func TestReconcileFailsEventWhenRunIsGone(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventRunStarted, time.Now())
	runID := "run-missing"
	event.RunID = &runID
	store.addEvent(event)

	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventFailed {
		t.Fatalf("event.Status = %v, want failed", got.Status)
	}
}

func TestReconcileCoalesceQueueSupersedesOlderEventsForSameItem(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencyCoalesce))

	base := time.Now()
	older := testEvent("event-older", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, base)
	newer := testEvent("event-newer", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, base.Add(time.Minute))
	store.addEvent(older)
	store.addEvent(newer)

	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	gotOlder := store.getEvent("event-older")
	if gotOlder.Status != protocol.GitHubMonitorEventSkipped {
		t.Fatalf("older event.Status = %v, want skipped (superseded)", gotOlder.Status)
	}
	gotNewer := store.getEvent("event-newer")
	if gotNewer.Status != protocol.GitHubMonitorEventRunStarted {
		t.Fatalf("newer event.Status = %v, want run_started (dispatched)", gotNewer.Status)
	}
}

func TestReconcileCoalesceQueueDoesNotSupersedeSkipPolicyEvents(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencySkip))

	base := time.Now()
	first := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, base)
	second := testEvent("event-2", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, base.Add(time.Minute))
	store.addEvent(first)
	store.addEvent(second)

	// Both would attempt to dispatch (order is not guaranteed since fakeRuns
	// always succeeds); the point of this test is that a "skip" rule's
	// events are never superseded by the coalescing pass.
	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, id := range []string{"event-1", "event-2"} {
		if got := store.getEvent(id).Status; got == protocol.GitHubMonitorEventSkipped {
			t.Fatalf("%s was superseded, but its rule uses concurrencyPolicy=skip, not coalesce", id)
		}
	}
}

func TestReconcileCoalesceQueueEnforcesPerRepositoryLimit(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	monitor := testMonitor("/repo")
	monitor.CoalesceQueueLimit = 1
	store.addMonitor(monitor)
	store.addRule(testRule("rule-a", "/repo", protocol.GitHubConcurrencyCoalesce))
	store.addRule(testRule("rule-b", "/repo", protocol.GitHubConcurrencyCoalesce))

	base := time.Now()
	// Two distinct (rule, item) groups; only one may survive the limit of 1.
	older := testEvent("event-a", "/repo", "rule-a", protocol.GitHubMonitorEventQueued, base)
	newer := testEvent("event-b", "/repo", "rule-b", protocol.GitHubMonitorEventQueued, base.Add(time.Minute))
	store.addEvent(older)
	store.addEvent(newer)

	dispatcher := NewDispatcher(store, &fakeSessions{}, &fakeRuns{})
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	gotOlder := store.getEvent("event-a")
	if gotOlder.Status != protocol.GitHubMonitorEventSkipped {
		t.Fatalf("event-a.Status = %v, want skipped (queue limit reached, it is the older group)", gotOlder.Status)
	}
	gotNewer := store.getEvent("event-b")
	if gotNewer.Status != protocol.GitHubMonitorEventRunStarted {
		t.Fatalf("event-b.Status = %v, want run_started (within the limit)", gotNewer.Status)
	}
}

func TestDispatchQueuedFailsWhenSessionCreationFails(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.addMonitor(testMonitor("/repo"))
	store.addRule(testRule("rule-1", "/repo", protocol.GitHubConcurrencyCoalesce))
	event := testEvent("event-1", "/repo", "rule-1", protocol.GitHubMonitorEventQueued, time.Now())
	store.addEvent(event)

	sessions := &fakeSessions{err: errors.New("workspace is not a git repository")}
	dispatcher := NewDispatcher(store, sessions, &fakeRuns{})
	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := store.getEvent("event-1")
	if got.Status != protocol.GitHubMonitorEventFailed {
		t.Fatalf("event.Status = %v, want failed", got.Status)
	}
}
