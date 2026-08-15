package protocol

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 1

type AgentName string

const AgentCodex AgentName = "codex"

type Provider struct {
	ID     AgentName `json:"id"`
	Label  string    `json:"label"`
	Models []string  `json:"models"`
}

type ProviderListResponse struct {
	Providers []Provider `json:"providers"`
}

type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionClosed SessionStatus = "closed"
)

type CleanupStatus string

const (
	CleanupNotStarted CleanupStatus = "not_started"
	CleanupPending    CleanupStatus = "pending"
	CleanupCompleted  CleanupStatus = "completed"
	CleanupFailed     CleanupStatus = "failed"
)

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunStarting  RunStatus = "starting"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

type AgentSession struct {
	ID               string        `json:"id"`
	Agent            AgentName     `json:"agent"`
	Workspace        string        `json:"workspace"`
	Worktree         string        `json:"worktree"`
	BaseCommit       string        `json:"baseCommit"`
	CodexThreadID    *string       `json:"codexThreadId,omitempty"`
	Status           SessionStatus `json:"status"`
	CreatedAt        time.Time     `json:"createdAt"`
	ClosedAt         *time.Time    `json:"closedAt,omitempty"`
	CleanupStatus    CleanupStatus `json:"cleanupStatus,omitempty"`
	CleanupError     *string       `json:"cleanupError,omitempty"`
	CleanupAttempts  int           `json:"cleanupAttempts,omitempty"`
	CleanupUpdatedAt *time.Time    `json:"cleanupUpdatedAt,omitempty"`
}

// SessionCursor is the decoded keyset position used for session pagination.
// Its wire representation is an opaque token owned by the HTTP API.
type SessionCursor struct {
	CreatedAt time.Time
	ID        string
}

type CreateSessionRequest struct {
	Agent     AgentName `json:"agent"`
	Workspace string    `json:"workspace"`
}

type SendMessageRequest struct {
	Message        string  `json:"message"`
	Model          *string `json:"model,omitempty"`
	TimeoutSeconds *int    `json:"timeoutSeconds,omitempty"`
}

type AgentRun struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"sessionId"`
	Status     RunStatus  `json:"status"`
	Prompt     string     `json:"prompt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
}

type TokenUsage struct {
	InputTokens           *int64 `json:"inputTokens,omitempty"`
	CachedInputTokens     *int64 `json:"cachedInputTokens,omitempty"`
	OutputTokens          *int64 `json:"outputTokens,omitempty"`
	ReasoningOutputTokens *int64 `json:"reasoningOutputTokens,omitempty"`
	TotalTokens           *int64 `json:"totalTokens,omitempty"`
	Source                string `json:"source"`
}

type EventSource string

const (
	EventSourceUser    EventSource = "user"
	EventSourceCodex   EventSource = "codex"
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
	EventTypeUserPrompt         = "user_prompt"
	EventTypeAssistantMessage   = "assistant_message"
	EventTypeReasoningSummary   = "reasoning_summary"
	EventTypeCommandStarted     = "command_started"
	EventTypeCommandCompleted   = "command_completed"
	EventTypeFileChangeReported = "file_change_reported"
	EventTypeChangeReviewed     = "change_reviewed"
	EventTypeUsageReported      = "usage_reported"
	EventTypeRunStarted         = "run_started"
	EventTypeRunCompleted       = "run_completed"
	EventTypeRunFailed          = "run_failed"
	EventTypeRunCancelled       = "run_cancelled"
	EventTypeError              = "error"
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

type ReviewStatus string

const (
	ReviewPending           ReviewStatus = "pending"
	ReviewPartiallyAccepted ReviewStatus = "partially_accepted"
	ReviewAccepted          ReviewStatus = "accepted"
	ReviewRejected          ReviewStatus = "rejected"
)

type ChangeHunk struct {
	ID           string       `json:"id"`
	OldStart     int          `json:"oldStart"`
	OldLines     int          `json:"oldLines"`
	NewStart     int          `json:"newStart"`
	NewLines     int          `json:"newLines"`
	OriginalText string       `json:"originalText"`
	ModifiedText string       `json:"modifiedText"`
	Status       ReviewStatus `json:"status"`
}

type FileChange struct {
	ID         string         `json:"id"`
	OldPath    *string        `json:"oldPath,omitempty"`
	NewPath    *string        `json:"newPath,omitempty"`
	Kind       FileChangeKind `json:"kind"`
	Original   *string        `json:"original,omitempty"`
	Modified   *string        `json:"modified,omitempty"`
	ReviewMode string         `json:"reviewMode"`
	Status     ReviewStatus   `json:"status"`
	Hunks      []ChangeHunk   `json:"hunks"`
}

type ChangeSet struct {
	SessionID string       `json:"sessionId"`
	Files     []FileChange `json:"files"`
}

type APIErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type APIErrorResponse struct {
	Error APIErrorBody `json:"error"`
}
