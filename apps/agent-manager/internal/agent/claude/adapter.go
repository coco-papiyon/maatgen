package claude

import (
	"context"
	"encoding/json"
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
		binaryName = "claude"
	}
	return &Adapter{binaryName: binaryName, runner: process.Runner{}}
}

func (*Adapter) Name() protocol.AgentName { return protocol.AgentClaude }

func (*Adapter) ParseLine(line string) agent.ParsedLine { return ParseLine(line) }

func (a *Adapter) Check(ctx context.Context) (agent.Info, error) {
	path, err := exec.LookPath(a.binaryName)
	if err != nil {
		return agent.Info{}, fmt.Errorf("%w: Claude Code executable was not found", ErrUnavailable)
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
		Path: path, Args: append(append([]string{}, a.prefixArgs...), "--version"), Timeout: 15 * time.Second,
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
	info := agent.Info{Name: protocol.AgentClaude, Path: path, Version: version}
	a.mu.Lock()
	a.info = info
	a.mu.Unlock()
	return info, nil
}

func (a *Adapter) Run(ctx context.Context, request agent.RunRequest, emit agent.Emitter) (agent.RunResult, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return agent.RunResult{}, errors.New("Claude Code prompt is required")
	}
	directory, err := filepath.Abs(request.Directory)
	if err != nil {
		return agent.RunResult{}, fmt.Errorf("resolve Claude Code repository: %w", err)
	}
	if file, err := os.Stat(directory); err != nil || !file.IsDir() {
		return agent.RunResult{}, errors.New("Claude Code repository must be an existing directory")
	}

	a.mu.RLock()
	info := a.info
	a.mu.RUnlock()
	if info.Path == "" {
		if info, err = a.Check(ctx); err != nil {
			return agent.RunResult{}, err
		}
	}

	// The prompt travels on stdin so repository content in the message is not
	// exposed through the process argument list or truncated by command line
	// length limits.
	args := append([]string{}, a.prefixArgs...)
	args = append(args,
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
	)
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if request.ReasoningEffort != "" {
		args = append(args, "--effort", request.ReasoningEffort)
	}
	if request.ThreadID != "" {
		args = append(args, "--resume", request.ThreadID)
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	result, err := a.runner.Run(ctx, process.Spec{
		Path: info.Path, Args: args, Dir: directory, Stdin: request.Prompt, Timeout: timeout,
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

func (a *Adapter) GetUsage(ctx context.Context, directory string) (protocol.ProviderUsage, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("resolve Claude Code usage directory: %w", err)
	}
	if file, err := os.Stat(directory); err != nil || !file.IsDir() {
		return protocol.ProviderUsage{}, errors.New("Claude Code usage directory must be an existing directory")
	}
	a.mu.RLock()
	info := a.info
	a.mu.RUnlock()
	if info.Path == "" {
		if info, err = a.Check(ctx); err != nil {
			return protocol.ProviderUsage{}, err
		}
	}
	var outputLines []string
	args := append([]string{}, a.prefixArgs...)
	args = append(args, "--print", "/usage", "--output-format", "json")
	result, err := a.runner.Run(ctx, process.Spec{Path: info.Path, Args: args, Dir: directory, Timeout: 15 * time.Second}, func(item process.Output) error {
		if item.Stream == process.Stdout {
			outputLines = append(outputLines, item.Line)
		}
		return nil
	})
	if err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("Claude Code usage: %w", err)
	}
	if result.ExitCode != 0 {
		return protocol.ProviderUsage{}, fmt.Errorf("Claude Code usage exited with code %d", result.ExitCode)
	}
	var response struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	rawOutput := strings.Join(outputLines, "\n")
	if err := json.Unmarshal([]byte(rawOutput), &response); err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("decode Claude Code usage: %w", err)
	}
	if response.IsError {
		return protocol.ProviderUsage{}, errors.New("Claude Code usage command failed")
	}
	windows, err := parseUsageWindows(response.Result)
	if err != nil {
		// Some Claude Code versions place the human-readable usage text outside
		// result. Keep the parser tolerant of that output shape.
		windows, err = parseUsageWindows(rawOutput)
		if err != nil {
			return protocol.ProviderUsage{}, err
		}
	}
	return protocol.ProviderUsage{Provider: protocol.AgentClaude, Windows: windows, FetchedAt: time.Now().UTC()}, nil
}

var _ agent.Adapter = (*Adapter)(nil)
var _ agent.UsageReader = (*Adapter)(nil)
