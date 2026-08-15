package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

var ErrUnavailable = errors.New("agent CLI is unavailable")

type Info struct {
	Name    protocol.AgentName `json:"name"`
	Path    string             `json:"path"`
	Version string             `json:"version"`
}

type RunRequest struct {
	Directory string
	Prompt    string
	ThreadID  string
	Model     string
	Timeout   time.Duration
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

type NormalizedEvent struct {
	Type string
	Data json.RawMessage
}

type ParsedLine struct {
	RawJSON   json.RawMessage
	ThreadID  string
	Usage     *protocol.TokenUsage
	Events    []NormalizedEvent
	Malformed bool
	Ignored   bool
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
	ParseLine(line string) ParsedLine
}
