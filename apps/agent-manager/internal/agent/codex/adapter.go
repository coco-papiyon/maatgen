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
	if request.Approval == nil {
		return agent.RunResult{}, errors.New("Codex run requires a command approval handler")
	}
	return a.runAppServer(ctx, info.Path, a.prefixArgs, directory, request, emit)
}

func (a *Adapter) GetUsage(ctx context.Context, directory string) (protocol.ProviderUsage, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("resolve Codex usage directory: %w", err)
	}
	if file, err := os.Stat(directory); err != nil || !file.IsDir() {
		return protocol.ProviderUsage{}, errors.New("Codex usage directory must be an existing directory")
	}
	a.mu.RLock()
	info := a.info
	a.mu.RUnlock()
	if info.Path == "" {
		if info, err = a.Check(ctx); err != nil {
			return protocol.ProviderUsage{}, err
		}
	}
	connection, err := startAppServer(ctx, info.Path, a.prefixArgs, directory, agent.RunRequest{Directory: directory}, nil)
	if err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("start Codex app-server for usage: %w", err)
	}
	defer connection.close()
	var response codexRateLimitsResponse
	if err := connection.call(ctx, "account/rateLimits/read", map[string]any{}, &response); err != nil {
		return protocol.ProviderUsage{}, fmt.Errorf("read Codex rate limits: %w", err)
	}
	windows := response.windows()
	if len(windows) == 0 {
		return protocol.ProviderUsage{}, errors.New("Codex rate limits response contained no usage windows")
	}
	return protocol.ProviderUsage{Provider: protocol.AgentCodex, Windows: windows, FetchedAt: time.Now().UTC()}, nil
}

type codexRateLimitsResponse struct {
	RateLimits          *codexRateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID map[string]*codexRateLimitSnapshot `json:"rateLimitsByLimitId"`
}

type codexRateLimitSnapshot struct {
	Primary   *codexRateLimitWindow `json:"primary"`
	Secondary *codexRateLimitWindow `json:"secondary"`
}

type codexRateLimitWindow struct {
	UsedPercent      int   `json:"usedPercent"`
	RemainingPercent *int  `json:"remainingPercent"`
	ResetsAt         int64 `json:"resetsAt"`
}

func (r codexRateLimitsResponse) windows() []protocol.ProviderUsageWindow {
	snapshot := r.RateLimits
	if snapshot == nil {
		for _, candidate := range r.RateLimitsByLimitID {
			snapshot = candidate
			break
		}
	}
	if snapshot == nil {
		return nil
	}
	windows := make([]protocol.ProviderUsageWindow, 0, 2)
	for _, item := range []struct {
		name   string
		window *codexRateLimitWindow
	}{
		{name: "primary", window: snapshot.Primary},
		{name: "secondary", window: snapshot.Secondary},
	} {
		if item.window == nil {
			continue
		}
		resetLabel := ""
		if item.window.ResetsAt > 0 {
			resetLabel = time.Unix(item.window.ResetsAt, 0).Local().Format(time.RFC3339)
		}
		usedPercent := min(100, max(0, item.window.UsedPercent))
		remainingPercent := 100 - usedPercent
		if item.window.RemainingPercent != nil {
			remainingPercent = min(100, max(0, *item.window.RemainingPercent))
		}
		windows = append(windows, protocol.ProviderUsageWindow{
			Name: item.name, UsedPercent: usedPercent,
			RemainingPercent: remainingPercent, ResetLabel: resetLabel,
		})
	}
	return windows
}

var _ agent.Adapter = (*Adapter)(nil)
var _ agent.UsageReader = (*Adapter)(nil)
