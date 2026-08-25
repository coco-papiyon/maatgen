// Package githubcontroller is the HTTP-facing orchestration layer for
// ADR-007 GitHub monitoring: it resolves a local workspace to a GitHub
// repository, and wires the SQLite store, the Poller
// (internal/githubmonitor), and the GitHub adapter (internal/githubapi)
// together for the Web API (internal/server) to call. It holds no
// business logic of its own beyond request validation and translating
// between local workspace paths and persisted repository identity.
package githubcontroller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubapi"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubmonitor"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/gitworktree"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// defaultCoalesceQueueLimit mirrors the SQLite column default (migration
// 012_github_monitoring.sql) for requests that omit it.
const defaultCoalesceQueueLimit = 20

var (
	ErrInvalidRequest = errors.New("invalid github monitor request")
	// ErrAmbiguousRemote is returned when creating or updating a monitor
	// without specifying remoteName while the repository has multiple
	// GitHub remotes and none of them is "origin" (ADR-007 section 1).
	ErrAmbiguousRemote = errors.New("multiple GitHub remotes found; a remoteName must be specified")
)

// RepositoryValidator resolves and normalizes a local workspace path to an
// absolute repository root, the same way session creation does. It is
// satisfied by *checkpoint.Manager.
type RepositoryValidator interface {
	ValidateRepository(ctx context.Context, workspace string) (string, error)
}

// Store is the persistence dependency this package needs. It is satisfied
// by *sqlite.Store.
type Store interface {
	CreateRepositoryMonitor(ctx context.Context, monitor protocol.GitHubRepositoryMonitor) error
	GetRepositoryMonitor(ctx context.Context, repository string) (protocol.GitHubRepositoryMonitor, error)
	ListRepositoryMonitors(ctx context.Context) ([]protocol.GitHubRepositoryMonitor, error)
	UpdateRepositoryMonitorSettings(ctx context.Context, monitor protocol.GitHubRepositoryMonitor, updatedAt time.Time) error
	DeleteRepositoryMonitor(ctx context.Context, repository string) error

	CreateTriggerRule(ctx context.Context, rule protocol.GitHubTriggerRule) error
	UpdateTriggerRule(ctx context.Context, rule protocol.GitHubTriggerRule) error
	GetTriggerRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error)
	ListTriggerRules(ctx context.Context, repository string) ([]protocol.GitHubTriggerRule, error)
	ListAllTriggerRules(ctx context.Context) ([]protocol.GitHubTriggerRule, error)
	DeleteTriggerRule(ctx context.Context, id string) error

	ListMonitorEvents(ctx context.Context, repository string, limit int) ([]protocol.GitHubMonitorEvent, error)
	ListAllMonitorEvents(ctx context.Context, limit int) ([]protocol.GitHubMonitorEvent, error)
	GetMonitorEvent(ctx context.Context, id string) (protocol.GitHubMonitorEvent, error)
	SkipMonitorEvent(ctx context.Context, id, reason string, updatedAt time.Time) error
	CreateReplayEvent(ctx context.Context, originalEventID, newEventID string, createdAt time.Time) (protocol.GitHubMonitorEvent, error)
}

// Poller runs one GitHub sync cycle for a repository. It is satisfied by
// *githubmonitor.Poller.
type Poller interface {
	SyncRepository(ctx context.Context, repository string) (githubmonitor.SyncResult, error)
}

// GitHubClient is what the live ("画面表示取得") Issue/PR list and detail
// endpoints need. It is satisfied by *githubapi.Client.
type GitHubClient interface {
	ListIssues(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error)
	GetIssue(ctx context.Context, owner, repo string, number int) (protocol.GitHubItem, error)
	ListPullRequests(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error)
	GetPullRequest(ctx context.Context, owner, repo string, number int) (protocol.GitHubItem, error)
	FetchProjectFields(ctx context.Context, owner, repo string, kind protocol.GitHubItemKind, number int) ([]protocol.GitHubProjectFieldValue, error)
}

// ClientFactory builds a GitHubClient for host.
type ClientFactory func(host string) (GitHubClient, error)

type Service struct {
	store        Store
	validator    RepositoryValidator
	gitPath      string
	allowedHosts []string
	poller       Poller
	clients      ClientFactory
	newRuleID    func() (string, error)
	newEventID   func() (string, error)
	now          func() time.Time
}

func New(store Store, validator RepositoryValidator, gitPath string, allowedHosts []string, poller Poller, clients ClientFactory) *Service {
	return &Service{
		store: store, validator: validator, gitPath: gitPath, allowedHosts: allowedHosts,
		poller: poller, clients: clients,
		newRuleID:  func() (string, error) { return generateID("github_rule") },
		newEventID: func() (string, error) { return generateID("github_replay") },
		now:        time.Now,
	}
}

// ResolveRepository resolves workspace's GitHub remote(s) and reports
// whether a monitor already exists for it (ADR-007 section 1 and the "対象
// リポジトリ領域" of section 7).
func (s *Service) ResolveRepository(ctx context.Context, workspace string) (protocol.GitHubRepositoryResolution, error) {
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return protocol.GitHubRepositoryResolution{}, err
	}
	response := protocol.GitHubRepositoryResolution{Repository: repository, Candidates: []protocol.GitHubRemoteCandidate{}}
	resolution, err := gitworktree.ResolveGitHubRemote(ctx, s.gitPath, repository, s.allowedHosts)
	if err != nil && !errors.Is(err, gitworktree.ErrNoGitHubRemote) {
		return protocol.GitHubRepositoryResolution{}, err
	}
	for _, candidate := range resolution.Candidates {
		response.Candidates = append(response.Candidates, protocol.GitHubRemoteCandidate(candidate))
	}
	if resolution.Repository != nil {
		selected := protocol.GitHubRemoteCandidate(*resolution.Repository)
		response.Selected = &selected
	}
	if monitor, monitorErr := s.store.GetRepositoryMonitor(ctx, repository); monitorErr == nil {
		response.Monitor = &monitor
	}
	return response, nil
}

// CreateMonitor registers a new GitHubRepositoryMonitor for a workspace.
func (s *Service) CreateMonitor(ctx context.Context, request protocol.CreateGitHubMonitorRequest) (protocol.GitHubRepositoryMonitor, error) {
	repository, err := s.validator.ValidateRepository(ctx, request.Workspace)
	if err != nil {
		return protocol.GitHubRepositoryMonitor{}, err
	}
	if request.PollIntervalSeconds <= 0 {
		return protocol.GitHubRepositoryMonitor{}, fmt.Errorf("%w: pollIntervalSeconds must be positive", ErrInvalidRequest)
	}
	candidate, err := s.resolveCandidate(ctx, repository, request.RemoteName)
	if err != nil {
		return protocol.GitHubRepositoryMonitor{}, err
	}
	coalesceLimit := request.CoalesceQueueLimit
	if coalesceLimit <= 0 {
		coalesceLimit = defaultCoalesceQueueLimit
	}
	now := s.now().UTC()
	monitor := protocol.GitHubRepositoryMonitor{
		Repository: repository, Host: candidate.Host, Owner: candidate.Owner, Name: candidate.Name,
		RemoteName: candidate.RemoteName, ProjectName: strings.TrimSpace(request.ProjectName), Enabled: true, PollIntervalSeconds: request.PollIntervalSeconds,
		CoalesceQueueLimit: coalesceLimit, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		return protocol.GitHubRepositoryMonitor{}, err
	}
	return monitor, nil
}

// UpdateMonitor replaces a monitor's editable settings.
func (s *Service) UpdateMonitor(ctx context.Context, request protocol.UpdateGitHubMonitorRequest) (protocol.GitHubRepositoryMonitor, error) {
	repository, err := s.validator.ValidateRepository(ctx, request.Workspace)
	if err != nil {
		return protocol.GitHubRepositoryMonitor{}, err
	}
	if request.PollIntervalSeconds <= 0 {
		return protocol.GitHubRepositoryMonitor{}, fmt.Errorf("%w: pollIntervalSeconds must be positive", ErrInvalidRequest)
	}
	existing, err := s.store.GetRepositoryMonitor(ctx, repository)
	if err != nil {
		return protocol.GitHubRepositoryMonitor{}, err
	}
	updated := existing
	updated.Enabled = request.Enabled
	updated.PollIntervalSeconds = request.PollIntervalSeconds
	coalesceLimit := request.CoalesceQueueLimit
	if coalesceLimit <= 0 {
		coalesceLimit = defaultCoalesceQueueLimit
	}
	updated.CoalesceQueueLimit = coalesceLimit
	updated.ProjectName = strings.TrimSpace(request.ProjectName)
	if request.RemoteName != nil {
		candidate, err := s.resolveCandidate(ctx, repository, request.RemoteName)
		if err != nil {
			return protocol.GitHubRepositoryMonitor{}, err
		}
		updated.Host, updated.Owner, updated.Name, updated.RemoteName = candidate.Host, candidate.Owner, candidate.Name, candidate.RemoteName
	} else {
		candidate, err := s.resolveCandidate(ctx, repository, &existing.RemoteName)
		if err != nil {
			return protocol.GitHubRepositoryMonitor{}, err
		}
		if candidate.Host != existing.Host || candidate.Owner != existing.Owner || candidate.Name != existing.Name {
			updated.Host, updated.Owner, updated.Name = candidate.Host, candidate.Owner, candidate.Name
		}
	}
	now := s.now().UTC()
	if err := s.store.UpdateRepositoryMonitorSettings(ctx, updated, now); err != nil {
		return protocol.GitHubRepositoryMonitor{}, err
	}
	updated.UpdatedAt = now
	return updated, nil
}

func (s *Service) resolveCandidate(ctx context.Context, repository string, remoteName *string) (gitworktree.GitHubRepository, error) {
	resolution, err := gitworktree.ResolveGitHubRemote(ctx, s.gitPath, repository, s.allowedHosts)
	if err != nil {
		return gitworktree.GitHubRepository{}, err
	}
	if remoteName != nil {
		for _, candidate := range resolution.Candidates {
			if candidate.RemoteName == *remoteName {
				return candidate, nil
			}
		}
		return gitworktree.GitHubRepository{}, fmt.Errorf("%w: remote %q is not a GitHub remote for this repository", ErrInvalidRequest, *remoteName)
	}
	if resolution.Repository == nil {
		return gitworktree.GitHubRepository{}, ErrAmbiguousRemote
	}
	return *resolution.Repository, nil
}

func (s *Service) GetMonitor(ctx context.Context, workspace string) (protocol.GitHubRepositoryMonitor, error) {
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return protocol.GitHubRepositoryMonitor{}, err
	}
	return s.store.GetRepositoryMonitor(ctx, repository)
}

func (s *Service) DeleteMonitor(ctx context.Context, workspace string) error {
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return err
	}
	return s.store.DeleteRepositoryMonitor(ctx, repository)
}

// SyncNow runs one poll cycle immediately (ADR-007 section 7's "今すぐ同
// 期"). It uses the same Poller a scheduled background poll would.
func (s *Service) SyncNow(ctx context.Context, workspace string) (protocol.GitHubSyncResult, error) {
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return protocol.GitHubSyncResult{}, err
	}
	result, err := s.poller.SyncRepository(ctx, repository)
	if err != nil {
		return protocol.GitHubSyncResult{}, err
	}
	return protocol.GitHubSyncResult{
		IssuesProcessed: result.IssuesProcessed, PullRequestsProcessed: result.PullRequestsProcessed,
		EventsMatched: result.EventsMatched,
	}, nil
}

// ListRules returns the trigger rules for workspace's repository, or, when
// workspace is empty, every rule across every registered repository (the
// Settings screen's cross-repository rule table).
func (s *Service) ListRules(ctx context.Context, workspace string) ([]protocol.GitHubTriggerRule, error) {
	if workspace == "" {
		return s.store.ListAllTriggerRules(ctx)
	}
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return nil, err
	}
	return s.store.ListTriggerRules(ctx, repository)
}

// ListMonitors returns every registered repository monitor (the Settings
// screen's multi-repository table), regardless of which repository is
// currently selected in the UI.
func (s *Service) ListMonitors(ctx context.Context) ([]protocol.GitHubRepositoryMonitor, error) {
	return s.store.ListRepositoryMonitors(ctx)
}

func (s *Service) CreateRule(ctx context.Context, request protocol.GitHubTriggerRuleRequest) (protocol.GitHubTriggerRule, error) {
	repository, err := s.validator.ValidateRepository(ctx, request.Workspace)
	if err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	if err := validateRuleRequest(request); err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	id, err := s.newRuleID()
	if err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	now := s.now().UTC()
	rule := protocol.GitHubTriggerRule{
		ID: id, Repository: repository, Name: strings.TrimSpace(request.Name), Enabled: request.Enabled,
		EventKinds: request.EventKinds, Filters: request.Filters, PromptTemplate: request.PromptTemplate,
		IncludeBody: request.IncludeBody, Provider: request.Provider, Model: request.Model,
		ReasoningEffort: request.ReasoningEffort, ConcurrencyPolicy: normalizeConcurrencyPolicy(request.ConcurrencyPolicy),
		Priority:  normalizeJobPriority(request.Priority),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateTriggerRule(ctx, rule); err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	return rule, nil
}

func (s *Service) GetRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error) {
	return s.store.GetTriggerRule(ctx, id)
}

func (s *Service) UpdateRule(ctx context.Context, id string, request protocol.GitHubTriggerRuleRequest) (protocol.GitHubTriggerRule, error) {
	existing, err := s.store.GetTriggerRule(ctx, id)
	if err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	repository, err := s.validator.ValidateRepository(ctx, request.Workspace)
	if err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	if err := validateRuleRequest(request); err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	updated := existing
	updated.Repository = repository
	updated.Name = strings.TrimSpace(request.Name)
	updated.Enabled = request.Enabled
	updated.EventKinds = request.EventKinds
	updated.Filters = request.Filters
	updated.PromptTemplate = request.PromptTemplate
	updated.IncludeBody = request.IncludeBody
	updated.Provider = request.Provider
	updated.Model = request.Model
	updated.ReasoningEffort = request.ReasoningEffort
	updated.ConcurrencyPolicy = normalizeConcurrencyPolicy(request.ConcurrencyPolicy)
	updated.Priority = normalizeJobPriority(request.Priority)
	updated.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateTriggerRule(ctx, updated); err != nil {
		return protocol.GitHubTriggerRule{}, err
	}
	return updated, nil
}

func (s *Service) DeleteRule(ctx context.Context, id string) error {
	return s.store.DeleteTriggerRule(ctx, id)
}

func normalizeConcurrencyPolicy(policy protocol.GitHubConcurrencyPolicy) protocol.GitHubConcurrencyPolicy {
	if policy == "" {
		return protocol.GitHubConcurrencyCoalesce
	}
	return policy
}

func normalizeJobPriority(priority protocol.GitHubJobPriority) protocol.GitHubJobPriority {
	if priority == "" {
		return protocol.GitHubPriorityMedium
	}
	return priority
}

func validateRuleRequest(request protocol.GitHubTriggerRuleRequest) error {
	if strings.TrimSpace(request.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	if len(request.EventKinds) == 0 {
		return fmt.Errorf("%w: eventKinds is required", ErrInvalidRequest)
	}
	for _, kind := range request.EventKinds {
		if kind != protocol.GitHubItemIssue && kind != protocol.GitHubItemPullRequest {
			return fmt.Errorf("%w: eventKinds contains an invalid value %q", ErrInvalidRequest, kind)
		}
	}
	if strings.TrimSpace(request.PromptTemplate) == "" {
		return fmt.Errorf("%w: promptTemplate is required", ErrInvalidRequest)
	}
	if _, err := template.New("validate").Option("missingkey=zero").Parse(request.PromptTemplate); err != nil {
		return fmt.Errorf("%w: promptTemplate is invalid: %v", ErrInvalidRequest, err)
	}
	switch request.Provider {
	case protocol.AgentCodex, protocol.AgentClaude, protocol.AgentCopilot:
	default:
		return fmt.Errorf("%w: provider is invalid", ErrInvalidRequest)
	}
	switch request.ConcurrencyPolicy {
	case "", protocol.GitHubConcurrencySkip, protocol.GitHubConcurrencyCoalesce:
	default:
		return fmt.Errorf("%w: concurrencyPolicy is invalid", ErrInvalidRequest)
	}
	switch request.Priority {
	case "", protocol.GitHubPriorityHigh, protocol.GitHubPriorityMedium, protocol.GitHubPriorityLow:
	default:
		return fmt.Errorf("%w: priority is invalid", ErrInvalidRequest)
	}
	return nil
}

// ListEvents returns the events for workspace's repository, or, when
// workspace is empty, the most recent events across every registered
// repository (the Job screen's cross-repository event table).
func (s *Service) ListEvents(ctx context.Context, workspace string, limit int) ([]protocol.GitHubMonitorEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if workspace == "" {
		return s.store.ListAllMonitorEvents(ctx, limit)
	}
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return nil, err
	}
	return s.store.ListMonitorEvents(ctx, repository, limit)
}

// ReplayEvent re-executes a past monitor event as a new Outbox delivery
// (ADR-007 section 6's "このイベントを実行"), without touching the
// original event's dedupe key.
func (s *Service) ReplayEvent(ctx context.Context, eventID string) (protocol.GitHubMonitorEvent, error) {
	newID, err := s.newEventID()
	if err != nil {
		return protocol.GitHubMonitorEvent{}, err
	}
	return s.store.CreateReplayEvent(ctx, eventID, newID, s.now().UTC())
}

// SkipEvent excludes an unstarted monitor event from automatic execution while
// retaining it in the event history with the normal "skipped" status.
func (s *Service) SkipEvent(ctx context.Context, eventID string) (protocol.GitHubMonitorEvent, error) {
	if err := s.store.SkipMonitorEvent(ctx, eventID, "manually skipped by user", s.now().UTC()); err != nil {
		return protocol.GitHubMonitorEvent{}, err
	}
	return s.store.GetMonitorEvent(ctx, eventID)
}

// ItemQuery narrows a live Issue/PR list request. All fields are optional;
// an empty ItemQuery returns every item in State (default "open").
type ItemQuery struct {
	State    string
	Assignee string
	Author   string
	Labels   []string
	Text     string
	Project  string
	Status   string
}

func (s *Service) ListIssues(ctx context.Context, workspace string, query ItemQuery) (protocol.GitHubItemListResponse, error) {
	return s.listItems(ctx, workspace, query, false)
}

func (s *Service) ListPullRequests(ctx context.Context, workspace string, query ItemQuery) (protocol.GitHubItemListResponse, error) {
	return s.listItems(ctx, workspace, query, true)
}

// listItems fetches Issues or Pull Requests fresh from GitHub for this
// response only (ADR-007 section 2: "画面表示取得" is never persisted).
// Project data is fetched per item only when the query filters on it,
// since it is otherwise unnecessary API traffic for a screen that mostly
// shows title/state/labels.
func (s *Service) listItems(ctx context.Context, workspace string, query ItemQuery, pullRequests bool) (protocol.GitHubItemListResponse, error) {
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return protocol.GitHubItemListResponse{}, err
	}
	monitor, err := s.store.GetRepositoryMonitor(ctx, repository)
	if err != nil {
		return protocol.GitHubItemListResponse{}, err
	}
	client, err := s.clients(monitor.Host)
	if err != nil {
		return protocol.GitHubItemListResponse{}, err
	}

	state := query.State
	if state == "" {
		state = "open"
	}
	opts := githubapi.ListOptions{State: state}
	var items []protocol.GitHubItem
	if pullRequests {
		items, err = client.ListPullRequests(ctx, monitor.Owner, monitor.Name, opts)
	} else {
		items, err = client.ListIssues(ctx, monitor.Owner, monitor.Name, opts)
	}
	if err != nil {
		return protocol.GitHubItemListResponse{}, err
	}

	needsProjects := query.Project != "" || query.Status != ""
	projectsUnavailable := false
	filtered := make([]protocol.GitHubItem, 0, len(items))
	for _, item := range items {
		if needsProjects {
			fields, fetchErr := client.FetchProjectFields(ctx, monitor.Owner, monitor.Name, item.Kind, item.Number)
			if fetchErr != nil {
				item.ProjectsError = fetchErr.Error()
				projectsUnavailable = true
			} else {
				item.ProjectFields = fields
			}
		}
		if matchesItemQuery(item, query) {
			filtered = append(filtered, item)
		}
	}
	return protocol.GitHubItemListResponse{Items: filtered, FetchedAt: s.now().UTC(), ProjectsUnavailable: projectsUnavailable}, nil
}

func matchesItemQuery(item protocol.GitHubItem, query ItemQuery) bool {
	if query.Assignee != "" {
		matched := false
		for _, assignee := range item.Assignees {
			if strings.EqualFold(assignee.Login, query.Assignee) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if query.Author != "" && !strings.EqualFold(item.Author.Login, query.Author) {
		return false
	}
	for _, label := range query.Labels {
		matched := false
		for _, itemLabel := range item.Labels {
			if strings.EqualFold(itemLabel.Name, label) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if query.Text != "" {
		needle := strings.ToLower(query.Text)
		if !strings.Contains(strings.ToLower(item.Title), needle) && !strings.Contains(strings.ToLower(item.Body), needle) {
			return false
		}
	}
	if query.Project != "" || query.Status != "" {
		if !item.HasProjectData() {
			return false
		}
		matched := false
		for _, field := range item.ProjectFields {
			if query.Project != "" && !strings.EqualFold(field.ProjectTitle, query.Project) {
				continue
			}
			if query.Status != "" && (!strings.EqualFold(field.FieldName, "Status") || !strings.EqualFold(field.Value, query.Status)) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func (s *Service) GetIssue(ctx context.Context, workspace string, number int) (protocol.GitHubItem, error) {
	return s.getItem(ctx, workspace, number, false)
}

func (s *Service) GetPullRequest(ctx context.Context, workspace string, number int) (protocol.GitHubItem, error) {
	return s.getItem(ctx, workspace, number, true)
}

func (s *Service) getItem(ctx context.Context, workspace string, number int, pullRequest bool) (protocol.GitHubItem, error) {
	repository, err := s.validator.ValidateRepository(ctx, workspace)
	if err != nil {
		return protocol.GitHubItem{}, err
	}
	monitor, err := s.store.GetRepositoryMonitor(ctx, repository)
	if err != nil {
		return protocol.GitHubItem{}, err
	}
	client, err := s.clients(monitor.Host)
	if err != nil {
		return protocol.GitHubItem{}, err
	}
	var item protocol.GitHubItem
	if pullRequest {
		item, err = client.GetPullRequest(ctx, monitor.Owner, monitor.Name, number)
	} else {
		item, err = client.GetIssue(ctx, monitor.Owner, monitor.Name, number)
	}
	if err != nil {
		return protocol.GitHubItem{}, err
	}
	fields, fetchErr := client.FetchProjectFields(ctx, monitor.Owner, monitor.Name, item.Kind, number)
	if fetchErr != nil {
		item.ProjectsError = fetchErr.Error()
	} else {
		item.ProjectFields = fields
	}
	return item, nil
}

func generateID(prefix string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(random), nil
}
