package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/process"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

const DefaultTimeout = 30 * time.Minute

var ErrUnavailable = agent.ErrUnavailable

type processRunner interface {
	Run(ctx context.Context, spec process.Spec, handler process.Handler) (process.Result, error)
}

type Adapter struct {
	binaryName string
	prefixArgs []string
	runner     processRunner

	mu   sync.RWMutex
	info agent.Info
}

func New(binaryName string) *Adapter {
	if strings.TrimSpace(binaryName) == "" {
		binaryName = "codex"
	}
	return &Adapter{binaryName: binaryName, runner: process.Runner{}}
}

func (*Adapter) Name() protocol.AgentName { return protocol.AgentCodex }

func (*Adapter) ParseLine(line string) agent.ParsedLine { return ParseLine(line) }

func (a *Adapter) Check(ctx context.Context) (agent.Info, error) {
	path, err := exec.LookPath(a.binaryName)
	if err != nil {
		return agent.Info{}, fmt.Errorf("%w: executable was not found", ErrUnavailable)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return agent.Info{}, fmt.Errorf("%w: resolve executable: %v", ErrUnavailable, err)
	}
	file, err := os.Stat(path)
	if err != nil || file.IsDir() {
		return agent.Info{}, fmt.Errorf("%w: executable path is invalid", ErrUnavailable)
	}

	var versionLines []string
	result, err := a.runner.Run(ctx, process.Spec{
		Path:    path,
		Args:    append(append([]string{}, a.prefixArgs...), "--version"),
		Timeout: 5 * time.Second,
	}, func(output process.Output) error {
		if output.Stream == process.Stdout {
			versionLines = append(versionLines, output.Line)
		}
		return nil
	})
	if err != nil {
		return agent.Info{}, fmt.Errorf("%w: version check: %v", ErrUnavailable, err)
	}
	version := strings.TrimSpace(strings.Join(versionLines, "\n"))
	if result.ExitCode != 0 || version == "" {
		return agent.Info{}, fmt.Errorf("%w: version check exited with code %d", ErrUnavailable, result.ExitCode)
	}
	info := agent.Info{Name: protocol.AgentCodex, Path: path, Version: version}
	a.mu.Lock()
	a.info = info
	a.mu.Unlock()
	return info, nil
}

func (a *Adapter) Run(ctx context.Context, request agent.RunRequest, emit agent.Emitter) (agent.RunResult, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return agent.RunResult{}, errors.New("Codex prompt is required")
	}
	directory, err := filepath.Abs(request.Directory)
	if err != nil {
		return agent.RunResult{}, fmt.Errorf("resolve Codex repository: %w", err)
	}
	if file, err := os.Stat(directory); err != nil || !file.IsDir() {
		return agent.RunResult{}, errors.New("Codex repository must be an existing directory")
	}

	a.mu.RLock()
	info := a.info
	a.mu.RUnlock()
	if info.Path == "" {
		if info, err = a.Check(ctx); err != nil {
			return agent.RunResult{}, err
		}
	}
	if request.Approval != nil {
		return a.runAppServer(ctx, info.Path, directory, request, emit)
	}

	args := append([]string{}, a.prefixArgs...)
	args = append(args,
		"--ask-for-approval", "never",
		"--sandbox", "workspace-write",
		"--cd", directory,
	)
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	args = append(args, "exec")
	if request.ThreadID != "" {
		args = append(args, "resume", "--json", request.ThreadID, "-")
	} else {
		args = append(args, "--json", "-")
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	result, err := a.runner.Run(ctx, process.Spec{
		Path:    info.Path,
		Args:    args,
		Dir:     directory,
		Stdin:   request.Prompt,
		Timeout: timeout,
	}, func(output process.Output) error {
		if emit == nil {
			return nil
		}
		stream := agent.OutputStdout
		if output.Stream == process.Stderr {
			stream = agent.OutputStderr
		}
		return emit(agent.Output{Stream: stream, Line: output.Line})
	})
	return agent.RunResult{
		ExitCode: result.ExitCode, StartedAt: result.StartedAt, FinishedAt: result.FinishedAt,
		Canceled: result.Canceled, TimedOut: result.TimedOut,
	}, err
}

var _ agent.Adapter = (*Adapter)(nil)
