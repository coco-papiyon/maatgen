package protocol

import (
	"strings"
	"time"
)

// GitHubItemKind distinguishes Issues from Pull Requests within the shared
// normalized GitHubItem representation (ADR-007 section 2).
type GitHubItemKind string

const (
	GitHubItemIssue       GitHubItemKind = "issue"
	GitHubItemPullRequest GitHubItemKind = "pull_request"
)

type GitHubItemState string

const (
	GitHubItemOpen   GitHubItemState = "open"
	GitHubItemClosed GitHubItemState = "closed"
)

type GitHubUser struct {
	Login string `json:"login"`
}

type GitHubLabel struct {
	Name string `json:"name"`
}

type GitHubMilestone struct {
	Title string `json:"title"`
}

type GitHubBranchRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// GitHubProjectFieldValue is a single (Project, field) value observed on an
// item, e.g. project "Roadmap", field "Status", value "Ready".
type GitHubProjectFieldValue struct {
	ProjectTitle string `json:"projectTitle"`
	FieldName    string `json:"fieldName"`
	Value        string `json:"value"`
}

// GitHubPullRequestDetails holds the fields that exist only on Pull
// Requests, in addition to the Issue-shaped fields every GitHubItem carries.
type GitHubPullRequestDetails struct {
	Draft              bool            `json:"draft"`
	Base               GitHubBranchRef `json:"base"`
	Head               GitHubBranchRef `json:"head"`
	RequestedReviewers []GitHubUser    `json:"requestedReviewers"`
}

// GitHubItem is the normalized representation shared by Issues and Pull
// Requests (ADR-007 section 2). Kind discriminates which one this is;
// PullRequest is populated only when Kind == GitHubItemPullRequest.
//
// ProjectFields and ProjectsError describe the outcome of fetching Projects
// data independently of the item itself: ProjectFields is nil when Project
// information could not be retrieved (missing permission, Projects not
// used, a partial GraphQL error, ...), and is a non-nil (possibly empty)
// slice when Projects were retrieved successfully. Rule evaluation that
// depends on a Project field (e.g. Status = "Ready") must treat a nil
// ProjectFields as "unknown" and must not treat it as a non-match or a
// match; see ADR-007 section 2 and section 3.
type GitHubItem struct {
	Kind          GitHubItemKind            `json:"kind"`
	Number        int                       `json:"number"`
	Title         string                    `json:"title"`
	Body          string                    `json:"body"`
	State         GitHubItemState           `json:"state"`
	Author        GitHubUser                `json:"author"`
	Assignees     []GitHubUser              `json:"assignees"`
	Labels        []GitHubLabel             `json:"labels"`
	Milestone     *GitHubMilestone          `json:"milestone,omitempty"`
	CreatedAt     time.Time                 `json:"createdAt"`
	UpdatedAt     time.Time                 `json:"updatedAt"`
	URL           string                    `json:"url"`
	PullRequest   *GitHubPullRequestDetails `json:"pullRequest,omitempty"`
	ProjectFields []GitHubProjectFieldValue `json:"projectFields,omitempty"`
	ProjectsError string                    `json:"projectsError,omitempty"`
}

// HasProjectData reports whether Projects information was successfully
// retrieved for this item. It is false while ProjectsError is set or no
// fetch has been attempted, in which case Project-based rule conditions
// must not be evaluated as matching or not matching.
func (item GitHubItem) HasProjectData() bool {
	return item.ProjectFields != nil && item.ProjectsError == ""
}

// ProjectFieldValue returns the value of the named field within the named
// project, and whether it was found. Comparison is case-insensitive on
// both project title and field name, matching typical GitHub usage where
// case is not significant to users configuring a monitor.
func (item GitHubItem) ProjectFieldValue(projectTitle, fieldName string) (string, bool) {
	for _, field := range item.ProjectFields {
		if strings.EqualFold(field.ProjectTitle, projectTitle) && strings.EqualFold(field.FieldName, fieldName) {
			return field.Value, true
		}
	}
	return "", false
}

// GitHubRepositoryMonitor is a repository's GitHub polling configuration
// (ADR-007 section 3). Repository (the local repository's absolute,
// validated path, matching AgentSession.Workspace) is its identity: exactly
// one monitor exists per local repository, so there is no separate
// synthetic ID to keep in sync.
type GitHubRepositoryMonitor struct {
	Repository          string     `json:"repository"`
	Host                string     `json:"host"`
	Owner               string     `json:"owner"`
	Name                string     `json:"name"`
	RemoteName          string     `json:"remoteName"`
	ProjectName         string     `json:"projectName,omitempty"`
	Enabled             bool       `json:"enabled"`
	PollIntervalSeconds int        `json:"pollIntervalSeconds"`
	CoalesceQueueLimit  int        `json:"coalesceQueueLimit"`
	LastSyncedAt        *time.Time `json:"lastSyncedAt,omitempty"`
	NextSyncAt          *time.Time `json:"nextSyncAt,omitempty"`
	LastError           *string    `json:"lastError,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// GitHubConcurrencyPolicy controls what a GitHubTriggerRule does when the
// repository's execution lock (ADR-007 section 5) is already held.
type GitHubConcurrencyPolicy string

const (
	GitHubConcurrencySkip     GitHubConcurrencyPolicy = "skip"
	GitHubConcurrencyCoalesce GitHubConcurrencyPolicy = "coalesce"
)

// GitHubProjectFilterCondition matches a GitHubItem whose named Project
// field currently holds value (e.g. project "Roadmap", field "Status",
// value "Ready"). Items with no Project data yet fetched (see
// GitHubItem.HasProjectData) never match, regardless of value.
type GitHubProjectFilterCondition struct {
	ProjectTitle string `json:"projectTitle"`
	FieldName    string `json:"fieldName"`
	Value        string `json:"value"`
}

// GitHubMonitorFilters is the extensible condition set a GitHubTriggerRule
// evaluates against a detected change (ADR-007 section 3). All populated
// fields are ANDed together; a field left at its zero value/empty slice
// imposes no constraint. This shape is intentionally broader than any
// initial UI exposes, so new conditions can be added without a schema
// change (filters is stored as a single JSON column).
type GitHubMonitorFilters struct {
	Actions       []string                      `json:"actions,omitempty"`
	Numbers       []int                         `json:"numbers,omitempty"`
	TitleContains *string                       `json:"titleContains,omitempty"`
	BodyContains  *string                       `json:"bodyContains,omitempty"`
	Authors       []string                      `json:"authors,omitempty"`
	Assignees     []string                      `json:"assignees,omitempty"`
	Reviewers     []string                      `json:"reviewers,omitempty"`
	Labels        []string                      `json:"labels,omitempty"`
	Milestones    []string                      `json:"milestones,omitempty"`
	States        []GitHubItemState             `json:"states,omitempty"`
	Draft         *bool                         `json:"draft,omitempty"`
	BaseBranches  []string                      `json:"baseBranches,omitempty"`
	HeadBranches  []string                      `json:"headBranches,omitempty"`
	Project       *GitHubProjectFilterCondition `json:"project,omitempty"`
	CreatedAfter  *time.Time                    `json:"createdAfter,omitempty"`
	CreatedBefore *time.Time                    `json:"createdBefore,omitempty"`
	UpdatedAfter  *time.Time                    `json:"updatedAfter,omitempty"`
	UpdatedBefore *time.Time                    `json:"updatedBefore,omitempty"`
}

// GitHubTriggerRule pairs a condition set with the Prompt and Provider to
// run when it matches (ADR-007 section 3). Multiple rules may exist per
// repository; a rule fires independently of the others.
type GitHubTriggerRule struct {
	ID             string               `json:"id"`
	Repository     string               `json:"repository"`
	Name           string               `json:"name"`
	Enabled        bool                 `json:"enabled"`
	EventKinds     []GitHubItemKind     `json:"eventKinds"`
	Filters        GitHubMonitorFilters `json:"filters"`
	PromptTemplate string               `json:"promptTemplate"`
	// IncludeBody opts into exposing the Issue/PR body to the Prompt
	// template. It defaults to false: body text is untrusted external
	// input, so ADR-007 section 2 requires it be left out of the Prompt
	// unless a rule explicitly opts in.
	IncludeBody       bool                    `json:"includeBody"`
	Provider          AgentName               `json:"provider"`
	Model             *string                 `json:"model,omitempty"`
	ReasoningEffort   *string                 `json:"reasoningEffort,omitempty"`
	ConcurrencyPolicy GitHubConcurrencyPolicy `json:"concurrencyPolicy"`
	CreatedAt         time.Time               `json:"createdAt"`
	UpdatedAt         time.Time               `json:"updatedAt"`
}

// GitHubObservedItem is the last normalized state Maatgen recorded for one
// Issue or Pull Request, used to detect what changed on the next poll
// (ADR-007 section 3 and section 6). It is monitoring-only state: it must
// never back an Issue/PR list or detail screen (those always fetch fresh
// from GitHub, see ADR-007 section 2).
type GitHubObservedItem struct {
	Repository            string            `json:"repository"`
	Kind                  GitHubItemKind    `json:"kind"`
	Number                int               `json:"number"`
	StateHash             string            `json:"stateHash"`
	LastAction            string            `json:"lastAction"`
	ProjectsAvailable     bool              `json:"projectsAvailable"`
	Snapshot              GitHubItem        `json:"snapshot"`
	EvaluatedRuleVersions map[string]string `json:"evaluatedRuleVersions"`
	FirstSyncedAt         time.Time         `json:"firstSyncedAt"`
	ObservedAt            time.Time         `json:"observedAt"`
}

// GitHubMonitorEventStatus is the Outbox lifecycle state of a detected
// change, tracked independently of protocol.RunStatus (ADR-007 section 6).
type GitHubMonitorEventStatus string

const (
	GitHubMonitorEventDetected       GitHubMonitorEventStatus = "detected"
	GitHubMonitorEventMatched        GitHubMonitorEventStatus = "matched"
	GitHubMonitorEventQueued         GitHubMonitorEventStatus = "queued"
	GitHubMonitorEventSessionCreated GitHubMonitorEventStatus = "session_created"
	GitHubMonitorEventRunStarted     GitHubMonitorEventStatus = "run_started"
	GitHubMonitorEventSkipped        GitHubMonitorEventStatus = "skipped"
	GitHubMonitorEventCompleted      GitHubMonitorEventStatus = "completed"
	GitHubMonitorEventFailed         GitHubMonitorEventStatus = "failed"
	GitHubMonitorEventCancelled      GitHubMonitorEventStatus = "cancelled"
)

// GitHubMonitorEvent records one detected change and everything needed to
// evaluate it, deduplicate it, and (if it matched) trace it to the Session
// and Run it started (ADR-007 section 6).
//
// DeliveryKey is the ADR-007 section 6 dedupe key
// (repository+kind+number+event identity+rule ID). It is nil for events
// created by replaying an earlier event (ReplayOfEventID set): replays are
// manual, user-initiated re-executions and must never collide with, or
// consume, the original detection's dedupe key.
type GitHubMonitorEvent struct {
	ID              string                   `json:"id"`
	Repository      string                   `json:"repository"`
	RuleID          *string                  `json:"ruleId,omitempty"`
	Kind            GitHubItemKind           `json:"kind"`
	Number          int                      `json:"number"`
	Action          string                   `json:"action"`
	BeforeStateHash *string                  `json:"beforeStateHash,omitempty"`
	AfterStateHash  string                   `json:"afterStateHash"`
	DeliveryKey     *string                  `json:"deliveryKey,omitempty"`
	Status          GitHubMonitorEventStatus `json:"status"`
	SkipReason      *string                  `json:"skipReason,omitempty"`
	ReplayOfEventID *string                  `json:"replayOfEventId,omitempty"`
	ItemSnapshot    GitHubItem               `json:"itemSnapshot"`
	SessionID       *string                  `json:"sessionId,omitempty"`
	RunID           *string                  `json:"runId,omitempty"`
	LastError       *string                  `json:"lastError,omitempty"`
	CreatedAt       time.Time                `json:"createdAt"`
	UpdatedAt       time.Time                `json:"updatedAt"`
}

// GitHubRemoteCandidate identifies a GitHub repository resolved from one of
// a local repository's Git remotes (ADR-007 section 1). It mirrors
// gitworktree.GitHubRepository; protocol intentionally has no dependency on
// gitworktree, so the HTTP layer converts between the two.
type GitHubRemoteCandidate struct {
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Name       string `json:"name"`
	RemoteName string `json:"remoteName"`
}

// GitHubRepositoryResolution is the response to resolving a local
// workspace's GitHub remote (ADR-007 section 1 and section 7's "対象リポ
// ジトリ領域"). Selected is nil when the remotes are ambiguous or none
// points at an allowed GitHub host; Candidates lists everything found so
// the caller can let the user choose. Monitor is nil when this repository
// has no GitHubRepositoryMonitor registered yet.
type GitHubRepositoryResolution struct {
	Repository string                   `json:"repository"`
	Candidates []GitHubRemoteCandidate  `json:"candidates"`
	Selected   *GitHubRemoteCandidate   `json:"selected,omitempty"`
	Monitor    *GitHubRepositoryMonitor `json:"monitor,omitempty"`
}

// CreateGitHubMonitorRequest registers a new GitHubRepositoryMonitor for a
// local workspace. RemoteName must be supplied when the workspace's remotes
// are ambiguous (see GitHubRepositoryResolution.Candidates); otherwise it
// is optional and defaults to the automatically resolved remote.
type CreateGitHubMonitorRequest struct {
	Workspace           string  `json:"workspace"`
	RemoteName          *string `json:"remoteName,omitempty"`
	ProjectName         string  `json:"projectName,omitempty"`
	PollIntervalSeconds int     `json:"pollIntervalSeconds"`
	CoalesceQueueLimit  int     `json:"coalesceQueueLimit,omitempty"`
}

// UpdateGitHubMonitorRequest replaces a monitor's editable settings.
// Changing RemoteName re-resolves Host/Owner/Name from that remote.
type UpdateGitHubMonitorRequest struct {
	Workspace           string  `json:"workspace"`
	Enabled             bool    `json:"enabled"`
	PollIntervalSeconds int     `json:"pollIntervalSeconds"`
	CoalesceQueueLimit  int     `json:"coalesceQueueLimit"`
	RemoteName          *string `json:"remoteName,omitempty"`
	ProjectName         string  `json:"projectName,omitempty"`
}

// GitHubSyncResult reports the outcome of a manual "sync now" action
// (ADR-007 section 7).
type GitHubSyncResult struct {
	IssuesProcessed       int `json:"issuesProcessed"`
	PullRequestsProcessed int `json:"pullRequestsProcessed"`
	EventsMatched         int `json:"eventsMatched"`
}

// GitHubTriggerRuleRequest creates or updates a GitHubTriggerRule. Workspace
// identifies the target repository the same way CreateGitHubMonitorRequest
// does; it is ignored on update (a rule's repository never changes).
type GitHubTriggerRuleRequest struct {
	Workspace         string                  `json:"workspace"`
	Name              string                  `json:"name"`
	Enabled           bool                    `json:"enabled"`
	EventKinds        []GitHubItemKind        `json:"eventKinds"`
	Filters           GitHubMonitorFilters    `json:"filters"`
	PromptTemplate    string                  `json:"promptTemplate"`
	IncludeBody       bool                    `json:"includeBody"`
	Provider          AgentName               `json:"provider"`
	Model             *string                 `json:"model,omitempty"`
	ReasoningEffort   *string                 `json:"reasoningEffort,omitempty"`
	ConcurrencyPolicy GitHubConcurrencyPolicy `json:"concurrencyPolicy"`
}

type GitHubTriggerRuleListResponse struct {
	Rules []GitHubTriggerRule `json:"rules"`
}

// GitHubRepositoryMonitorListResponse lists every registered repository
// monitor, regardless of which repository is currently selected in the UI
// (multi-repository settings table).
type GitHubRepositoryMonitorListResponse struct {
	Monitors []GitHubRepositoryMonitor `json:"monitors"`
}

type GitHubMonitorEventListResponse struct {
	Events []GitHubMonitorEvent `json:"events"`
}

// GitHubItemListResponse is the response to a live ("画面表示取得") Issue
// or Pull Request list request: fetched fresh from GitHub for this
// response only, never persisted (ADR-007 section 2). FetchedAt lets the
// UI show when the snapshot was taken. ProjectsUnavailable is true if
// Project data could not be fetched for one or more items (each such
// item's own ProjectsError still carries the detail); the list itself is
// still returned.
type GitHubItemListResponse struct {
	Items               []GitHubItem `json:"items"`
	FetchedAt           time.Time    `json:"fetchedAt"`
	ProjectsUnavailable bool         `json:"projectsUnavailable,omitempty"`
}
