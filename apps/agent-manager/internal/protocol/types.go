package protocol

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 2

type AgentName string

const (
	AgentCodex   AgentName = "codex"
	AgentCopilot AgentName = "copilot"
)

type Provider struct {
	ID           AgentName `json:"id"`
	Label        string    `json:"label"`
	Models       []string  `json:"models"`
	DefaultModel string    `json:"defaultModel,omitempty"`
}

type ProviderListResponse struct {
	Providers []Provider `json:"providers"`
}

type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionClosed SessionStatus = "closed"
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
	ID            string        `json:"id"`
	Agent         AgentName     `json:"agent"`
	Workspace     string        `json:"workspace"`
	AgentThreadID *string       `json:"agentThreadId,omitempty"`
	Status        SessionStatus `json:"status"`
	CreatedAt     time.Time     `json:"createdAt"`
	ClosedAt      *time.Time    `json:"closedAt,omitempty"`
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

type SessionUsage struct {
	SessionID string          `json:"sessionId"`
	Summary   TokenUsage      `json:"summary"`
	Runs      []RunUsageEntry `json:"runs"`
}

type RunUsageEntry struct {
	Run   AgentRun    `json:"run"`
	Usage *TokenUsage `json:"usage,omitempty"`
}

type EventSource string

const (
	EventSourceUser    EventSource = "user"
	EventSourceCodex   EventSource = "codex"
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
	EventTypeUserPrompt         = "user_prompt"
	EventTypeAssistantMessage   = "assistant_message"
	EventTypeReasoningSummary   = "reasoning_summary"
	EventTypeCommandStarted     = "command_started"
	EventTypeCommandCompleted   = "command_completed"
	EventTypeFileChangeReported = "file_change_reported"
	EventTypeCheckpointCreated  = "checkpoint_created"
	EventTypeChangeRestored     = "change_restored"
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
