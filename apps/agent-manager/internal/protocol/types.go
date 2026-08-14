package protocol

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 1

type AgentName string

const AgentCodex AgentName = "codex"

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
	Worktree      string        `json:"worktree"`
	BaseCommit    string        `json:"baseCommit"`
	CodexThreadID *string       `json:"codexThreadId,omitempty"`
	Status        SessionStatus `json:"status"`
	CreatedAt     time.Time     `json:"createdAt"`
	ClosedAt      *time.Time    `json:"closedAt,omitempty"`
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
