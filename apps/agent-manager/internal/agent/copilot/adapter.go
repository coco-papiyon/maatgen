package copilot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/process"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	copilotsdk "github.com/github/copilot-sdk/go"
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
		binaryName = "copilot"
	}
	return &Adapter{binaryName: binaryName, runner: process.Runner{}}
}

func (*Adapter) Name() protocol.AgentName { return protocol.AgentCopilot }

func (*Adapter) ParseLine(line string) agent.ParsedLine { return ParseLine(line) }

func (a *Adapter) Check(ctx context.Context) (agent.Info, error) {
	path, err := exec.LookPath(a.binaryName)
	if err != nil {
		return agent.Info{}, fmt.Errorf("%w: GitHub Copilot executable was not found", ErrUnavailable)
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
		Path: path, Args: append(append([]string{}, a.prefixArgs...), "--version"), Timeout: 5 * time.Second,
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
	info := agent.Info{Name: protocol.AgentCopilot, Path: path, Version: version}
	a.mu.Lock()
	a.info = info
	a.mu.Unlock()
	return info, nil
}

func (a *Adapter) Run(ctx context.Context, request agent.RunRequest, emit agent.Emitter) (agent.RunResult, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return agent.RunResult{}, errors.New("GitHub Copilot prompt is required")
	}
	directory, err := filepath.Abs(request.Directory)
	if err != nil {
		return agent.RunResult{}, fmt.Errorf("resolve GitHub Copilot repository: %w", err)
	}
	if file, err := os.Stat(directory); err != nil || !file.IsDir() {
		return agent.RunResult{}, errors.New("GitHub Copilot repository must be an existing directory")
	}

	a.mu.RLock()
	info := a.info
	a.mu.RUnlock()
	if info.Path == "" {
		if info, err = a.Check(ctx); err != nil {
			return agent.RunResult{}, err
		}
	}

	args := append([]string{}, a.prefixArgs...)
	args = append(args,
		"-C", directory,
		"--prompt", request.Prompt,
		"--output-format", "json",
		"--allow-all",
		"--no-ask-user",
		"--no-auto-update",
		"--no-color",
		"--no-remote",
		"--no-remote-export",
	)
	if request.ThreadID != "" {
		args = append(args, "--resume="+request.ThreadID)
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if request.ReasoningEffort != "" {
		args = append(args, "--effort="+request.ReasoningEffort)
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	result, err := a.runner.Run(ctx, process.Spec{
		Path: info.Path, Args: args, Dir: directory, Timeout: timeout,
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
		return protocol.ProviderUsage{}, fmt.Errorf("resolve GitHub Copilot usage directory: %w", err)
	}
	if file, err := os.Stat(directory); err != nil || !file.IsDir() {
		return protocol.ProviderUsage{}, errors.New("GitHub Copilot usage directory must be an existing directory")
	}
	a.mu.RLock()
	info := a.info
	a.mu.RUnlock()
	if info.Path == "" {
		if info, err = a.Check(ctx); err != nil {
			return protocol.ProviderUsage{}, err
		}
	}
	client := copilotsdk.NewClient(&copilotsdk.ClientOptions{
		Connection:       copilotsdk.StdioConnection{Path: info.Path, Args: append([]string{}, a.prefixArgs...)},
		WorkingDirectory: directory,
	})
	if err := client.Start(ctx); err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("start GitHub Copilot SDK: %w", err)
	}
	defer client.Stop()
	quota, err := client.RPC.Account.GetQuota(ctx, nil)
	if err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("read GitHub Copilot quota: %w", err)
	}
	windows := make([]protocol.ProviderUsageWindow, 0, len(quota.QuotaSnapshots))
	for name, snapshot := range quota.QuotaSnapshots {
		remaining := int(math.Round(snapshot.RemainingPercentage))
		if remaining < 0 {
			remaining = 0
		}
		if remaining > 100 {
			remaining = 100
		}
		resetLabel := ""
		if snapshot.ResetDate != nil {
			resetLabel = snapshot.ResetDate.Local().Format(time.RFC3339)
		}
		windows = append(windows, protocol.ProviderUsageWindow{
			Name: name, UsedPercent: 100 - remaining, RemainingPercent: remaining, ResetLabel: resetLabel,
		})
	}
	if len(windows) == 0 {
		return protocol.ProviderUsage{}, errors.New("GitHub Copilot quota response contained no usage windows")
	}
	slices.SortFunc(windows, func(left, right protocol.ProviderUsageWindow) int { return strings.Compare(left.Name, right.Name) })
	return protocol.ProviderUsage{Provider: protocol.AgentCopilot, Windows: windows, FetchedAt: time.Now().UTC()}, nil
}

var _ agent.Adapter = (*Adapter)(nil)
var _ agent.UsageReader = (*Adapter)(nil)
