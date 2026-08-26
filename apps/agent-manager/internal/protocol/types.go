package protocol

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 2

type AgentName string

const (
	AgentCodex   AgentName = "codex"
	AgentClaude  AgentName = "claude"
	AgentCopilot AgentName = "copilot"
)

type ModelPricingInfo struct {
	InputPerMillion  float64 `json:"inputPerMillion"`
	OutputPerMillion float64 `json:"outputPerMillion"`
}

type Provider struct {
	ID           AgentName                    `json:"id"`
	Label        string                       `json:"label"`
	Models       []string                     `json:"models"`
	DefaultModel string                       `json:"defaultModel,omitempty"`
	Pricing      map[string]*ModelPricingInfo `json:"pricing,omitempty"`
}

type ProviderListResponse struct {
	Providers []Provider `json:"providers"`
}

type ProviderUsageWindow struct {
	Name             string `json:"name"`
	UsedPercent      int    `json:"usedPercent"`
	RemainingPercent int    `json:"remainingPercent"`
	ResetLabel       string `json:"resetLabel,omitempty"`
}

type ProviderUsage struct {
	Provider  AgentName             `json:"provider"`
	Windows   []ProviderUsageWindow `json:"windows"`
	FetchedAt time.Time             `json:"fetchedAt"`
}

type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionClosed SessionStatus = "closed"
)

type RunStatus string

const (
	RunQueued             RunStatus = "queued"
	RunStarting           RunStatus = "starting"
	RunRunning            RunStatus = "running"
	RunWaitingForApproval RunStatus = "waiting_for_approval"
	RunCompleted          RunStatus = "completed"
	RunFailed             RunStatus = "failed"
	RunCancelled          RunStatus = "cancelled"
)

// TriggerSource identifies what initiated a Session or Run.
type TriggerSource string

const (
	TriggerSourceManual        TriggerSource = "manual"
	TriggerSourceGitHubMonitor TriggerSource = "github_monitor"
)

// AgentSession represents a conversation session with an Agent.
type AgentSession struct {
	ID                 string          `json:"id"`
	Agent              AgentName       `json:"agent"`
	Workspace          string          `json:"workspace"`
	AgentThreadID      *string         `json:"agentThreadId,omitempty"`
	Status             SessionStatus   `json:"status"`
	TriggerSource      TriggerSource   `json:"triggerSource"`
	GitHubMonitorEvent *string         `json:"githubMonitorEvent,omitempty"`
	GitHubRuleID       *string         `json:"githubRuleId,omitempty"`
	GitHubItemKind     *GitHubItemKind `json:"githubItemKind,omitempty"`
	GitHubItemNumber   *int            `json:"githubItemNumber,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	ClosedAt           *time.Time      `json:"closedAt,omitempty"`
	FirstPrompt        *string         `json:"firstPrompt,omitempty"`
}

// SessionCursor is the decoded keyset position used for session pagination.
// Its wire representation is an opaque token owned by the HTTP API.
type SessionCursor struct {
	CreatedAt time.Time
	ID        string
}

type CreateSessionRequest struct {
	Agent              AgentName       `json:"agent"`
	Workspace          string          `json:"workspace"`
	TriggerSource      TriggerSource   `json:"triggerSource"`
	GitHubMonitorEvent *string         `json:"githubMonitorEvent,omitempty"`
	GitHubRuleID       *string         `json:"githubRuleId,omitempty"`
	GitHubItemKind     *GitHubItemKind `json:"githubItemKind,omitempty"`
	GitHubItemNumber   *int            `json:"githubItemNumber,omitempty"`
}

type SendMessageRequest struct {
	Message         string  `json:"message"`
	Model           *string `json:"model,omitempty"`
	ReasoningEffort *string `json:"reasoningEffort,omitempty"`
	TimeoutSeconds  *int    `json:"timeoutSeconds,omitempty"`
	// AutoApprove requests that Codex's command execution approvals be
	// granted automatically for this run, bypassing the AI/human approval
	// flow (see agent.RunRequest.AutoApprove). Ignored by other providers.
	AutoApprove bool `json:"autoApprove,omitempty"`
	// AutoRetryOfRunID marks this Run as the automatic resume of a Run that
	// stopped after its provider reported a usage/session limit (ADR-008).
	// It is set internally by internal/usageretry, not by API clients.
	AutoRetryOfRunID *string `json:"autoRetryOfRunId,omitempty"`
}

type AgentRun struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"sessionId"`
	Status     RunStatus  `json:"status"`
	Prompt     string     `json:"prompt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	// AutoRetryOfRunID is set when this Run was started automatically to
	// resume another Run that stopped after hitting a usage/session limit
	// (ADR-008). It bounds automatic retries to one per original stop: a Run
	// that itself carries this field never schedules another automatic
	// retry, even if it also fails on a usage limit.
	AutoRetryOfRunID *string `json:"autoRetryOfRunId,omitempty"`
	// UsageLimitRetryPendingAt is set when this Run failed after its output
	// indicated a provider usage/session limit and it is eligible for one
	// automatic retry (ADR-008). internal/usageretry clears it once it has
	// either started the retry Run or given up.
	UsageLimitRetryPendingAt *time.Time `json:"usageLimitRetryPendingAt,omitempty"`
}

type ApprovalStatus string

const (
	ApprovalPending   ApprovalStatus = "pending"
	ApprovalApproved  ApprovalStatus = "approved"
	ApprovalDenied    ApprovalStatus = "denied"
	ApprovalCancelled ApprovalStatus = "cancelled"
	ApprovalExpired   ApprovalStatus = "expired"
)

type ApprovalRisk string

const (
	ApprovalRiskSafe     ApprovalRisk = "safe"
	ApprovalRiskLow      ApprovalRisk = "low"
	ApprovalRiskHigh     ApprovalRisk = "high"
	ApprovalRiskCritical ApprovalRisk = "critical"
)

type ApprovalScope string

const (
	ApprovalScopeOnce      ApprovalScope = "once"
	ApprovalScopeSession   ApprovalScope = "session"
	ApprovalScopePermanent ApprovalScope = "permanent"
)

type ApprovalDecision string

const (
	ApprovalAllowOnce      ApprovalDecision = "allow_once"
	ApprovalAllowSession   ApprovalDecision = "allow_session"
	ApprovalAllowPermanent ApprovalDecision = "allow_permanent"
	ApprovalDeny           ApprovalDecision = "deny"
)

type ApprovalSource string

const (
	ApprovalSourceConfig ApprovalSource = "config"
	ApprovalSourceAI     ApprovalSource = "ai"
	ApprovalSourceHuman  ApprovalSource = "human"
	ApprovalSourceSystem ApprovalSource = "system"
)

type CommandSegment struct {
	Index   int      `json:"index"`
	Command string   `json:"command"`
	Argv    []string `json:"argv"`
	Allowed bool     `json:"allowed"`
}

type CommandApproval struct {
	ID                string            `json:"id"`
	SessionID         string            `json:"sessionId"`
	RunID             string            `json:"runId"`
	ProviderRequestID string            `json:"providerRequestId"`
	Command           string            `json:"command"`
	Shell             string            `json:"shell"`
	WorkingDirectory  string            `json:"workingDirectory"`
	Segments          []CommandSegment  `json:"segments"`
	Status            ApprovalStatus    `json:"status"`
	Risk              *ApprovalRisk     `json:"risk,omitempty"`
	Confidence        *float64          `json:"confidence,omitempty"`
	Summary           *string           `json:"summary,omitempty"`
	Factors           []string          `json:"factors"`
	Decision          *ApprovalDecision `json:"decision,omitempty"`
	Scope             *ApprovalScope    `json:"scope,omitempty"`
	Source            *ApprovalSource   `json:"source,omitempty"`
	RuleArgv          []string          `json:"ruleArgv,omitempty"`
	CreatedAt         time.Time         `json:"createdAt"`
	DecidedAt         *time.Time        `json:"decidedAt,omitempty"`
}

type ApprovalListResponse struct {
	Approvals []CommandApproval `json:"approvals"`
}

type ApprovalDecisionRequest struct {
	Decision ApprovalDecision `json:"decision"`
	RuleArgv []string         `json:"ruleArgv,omitempty"`
}

type TokenUsage struct {
	InputTokens           *int64   `json:"inputTokens,omitempty"`
	CachedInputTokens     *int64   `json:"cachedInputTokens,omitempty"`
	OutputTokens          *int64   `json:"outputTokens,omitempty"`
	ReasoningOutputTokens *int64   `json:"reasoningOutputTokens,omitempty"`
	TotalTokens           *int64   `json:"totalTokens,omitempty"`
	Model                 *string  `json:"model,omitempty"`
	ActualModel           *string  `json:"actualModel,omitempty"`
	AICredits             *float64 `json:"aiCredits,omitempty"`
	CostUSD               *float64 `json:"costUsd,omitempty"`
	Source                string   `json:"source"`
}

type SessionUsage struct {
	SessionID string          `json:"sessionId"`
	Summary   TokenUsage      `json:"summary"`
	Runs      []RunUsageEntry `json:"runs"`
}

type RunUsageEntry struct {
	Run   AgentRun    `json:"run"`
	Usage *TokenUsage `json:"usage,omitempty"`
}

type UsageSeriesPoint struct {
	Key         string  `json:"key"`
	CostUSD     float64 `json:"costUsd"`
	AICredits   float64 `json:"aiCredits"`
	TotalTokens int64   `json:"totalTokens"`
}

type UsagePeriod struct {
	Period                string             `json:"period"`
	CostUSD               float64            `json:"costUsd"`
	AICredits             float64            `json:"aiCredits"`
	TotalTokens           int64              `json:"totalTokens"`
	InputTokens           int64              `json:"inputTokens"`
	CachedInputTokens     int64              `json:"cachedInputTokens"`
	OutputTokens          int64              `json:"outputTokens"`
	ReasoningOutputTokens int64              `json:"reasoningOutputTokens"`
	Series                []UsageSeriesPoint `json:"series"`
}

type UsageSummary struct {
	Granularity string        `json:"granularity"`
	Provider    *string       `json:"provider,omitempty"`
	Model       *string       `json:"model,omitempty"`
	SeriesBy    string        `json:"seriesBy"`
	Periods     []UsagePeriod `json:"periods"`
}

type UsageModelListResponse struct {
	Models []string `json:"models"`
}

type UsageProviderListResponse struct {
	Providers []string `json:"providers"`
}

type SourceStatsLanguage struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
	Blank    int    `json:"blank"`
	Comment  int    `json:"comment"`
	Code     int    `json:"code"`
}

type SourceStats struct {
	SessionID string                `json:"sessionId"`
	Languages []SourceStatsLanguage `json:"languages"`
	Total     SourceStatsLanguage   `json:"total"`
}

// WorkspaceFileNode is one entry in a workspace directory listing. Listings
// are non-recursive: a directory's HasChildren hint tells the client whether
// it's worth requesting that directory's contents on demand.
type WorkspaceFileNode struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "dir" | "file"
	HasChildren bool   `json:"hasChildren,omitempty"`
}

// WorkspaceFileContent is the text content of a single workspace file.
// Binary files are reported with Binary=true and no Content, and files
// beyond the read size limit are truncated with Truncated=true.
type WorkspaceFileContent struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

type EventSource string

const (
	EventSourceUser    EventSource = "user"
	EventSourceCodex   EventSource = "codex"
	EventSourceClaude  EventSource = "claude"
	EventSourceCopilot EventSource = "copilot"
	EventSourceManager EventSource = "manager"
)

type SessionEvent struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"sessionId"`
	RunID         *string         `json:"runId,omitempty"`
	Sequence      int64           `json:"sequence"`
	Timestamp     time.Time       `json:"timestamp"`
	SchemaVersion int             `json:"schemaVersion"`
	Source        EventSource     `json:"source"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
}

const (
	EventTypeUserPrompt               = "user_prompt"
	EventTypeAssistantMessage         = "assistant_message"
	EventTypeReasoningSummary         = "reasoning_summary"
	EventTypeCommandStarted           = "command_started"
	EventTypeCommandCompleted         = "command_completed"
	EventTypeCommandApprovalRequested = "command_approval_requested"
	EventTypeCommandApprovalDecided   = "command_approval_decided"
	EventTypeFileChangeReported       = "file_change_reported"
	EventTypeCheckpointCreated        = "checkpoint_created"
	EventTypeChangeRestored           = "change_restored"
	EventTypeUsageReported            = "usage_reported"
	EventTypeRunStarted               = "run_started"
	EventTypeRunCompleted             = "run_completed"
	EventTypeRunFailed                = "run_failed"
	EventTypeRunCancelled             = "run_cancelled"
	EventTypeError                    = "error"
)

type FileChangeKind string

const (
	FileModify     FileChangeKind = "modify"
	FileAdd        FileChangeKind = "add"
	FileDelete     FileChangeKind = "delete"
	FileRename     FileChangeKind = "rename"
	FileBinary     FileChangeKind = "binary"
	FileModeChange FileChangeKind = "mode_change"
)

type RestoreStatus string

const (
	RestoreChanged           RestoreStatus = "changed"
	RestorePartiallyRestored RestoreStatus = "partially_restored"
	RestoreRestored          RestoreStatus = "restored"
	RestoreConflict          RestoreStatus = "conflict"
)

type ChangeHunk struct {
	ID           string        `json:"id"`
	OldStart     int           `json:"oldStart"`
	OldLines     int           `json:"oldLines"`
	NewStart     int           `json:"newStart"`
	NewLines     int           `json:"newLines"`
	OriginalText string        `json:"originalText"`
	ModifiedText string        `json:"modifiedText"`
	Status       RestoreStatus `json:"status"`
}

type FileChange struct {
	ID          string         `json:"id"`
	OldPath     *string        `json:"oldPath,omitempty"`
	NewPath     *string        `json:"newPath,omitempty"`
	Kind        FileChangeKind `json:"kind"`
	Original    *string        `json:"original,omitempty"`
	Modified    *string        `json:"modified,omitempty"`
	RestoreMode string         `json:"restoreMode"`
	Status      RestoreStatus  `json:"status"`
	Hunks       []ChangeHunk   `json:"hunks"`
}

type ChangeSet struct {
	SessionID    string       `json:"sessionId"`
	RunID        string       `json:"runId"`
	CheckpointID string       `json:"checkpointId"`
	BeforeTree   string       `json:"beforeTree"`
	AfterTree    string       `json:"afterTree"`
	Files        []FileChange `json:"files"`
}

type Checkpoint struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"sessionId"`
	RunID       string     `json:"runId"`
	HeadCommit  string     `json:"headCommit"`
	IndexTree   string     `json:"indexTree"`
	BeforeTree  string     `json:"beforeTree"`
	AfterTree   *string    `json:"afterTree,omitempty"`
	BeforeRef   string     `json:"beforeRef"`
	AfterRef    *string    `json:"afterRef,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type APIErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type APIErrorResponse struct {
	Error APIErrorBody `json:"error"`
}
