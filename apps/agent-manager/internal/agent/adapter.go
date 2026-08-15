package agent

import (
	"context"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type Info struct {
	Name    protocol.AgentName `json:"name"`
	Path    string             `json:"path"`
	Version string             `json:"version"`
}

type RunRequest struct {
	Worktree string
	Prompt   string
	ThreadID string
	Model    string
	Timeout  time.Duration
}

type OutputStream string

const (
	OutputStdout OutputStream = "stdout"
	OutputStderr OutputStream = "stderr"
)

type Output struct {
	Stream OutputStream
	Line   string
}

type RunResult struct {
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Canceled   bool
	TimedOut   bool
}

type Emitter func(Output) error

type Adapter interface {
	Name() protocol.AgentName
	Check(ctx context.Context) (Info, error)
	Run(ctx context.Context, request RunRequest, emit Emitter) (RunResult, error)
}
