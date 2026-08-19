package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/claude"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/codex"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/copilot"
	approvalservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/approval"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/changeset"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/eventbroker"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/pricing"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	restoreservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/restore"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/runtimeinfo"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/server"
	sessionservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/sourcestats"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/toolconfig"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("agent manager stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	defaultWorkspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	host := flag.String("host", "127.0.0.1", "listen host")
	port := flag.Int("port", 3100, "listen port; use 0 for an ephemeral port")
	defaultDir, err := defaultDataDir()
	if err != nil {
		return err
	}
	dataDir := flag.String("data-dir", defaultDir, "directory for the database and runtime data")
	runtimeFile := flag.String("runtime-file", "", "path for runtime metadata; defaults to <data-dir>/runtime.json")
	authToken := flag.String("auth-token", "", "bearer token; generated when omitted")
	allowedOrigins := flag.String("allowed-origins", "http://localhost:5173,http://127.0.0.1:5173", "comma-separated browser origins")
	configFile := flag.String("config", toolconfig.DefaultRelativePath, "tool configuration path relative to the executable")
	backfillCosts := flag.Bool("backfill-costs", false, "refresh pricing and recalculate historical run costs, then exit")
	flag.Parse()
	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	toolConfig, resolvedConfigPath, err := toolconfig.LoadFrom(executablePath, *configFile, defaultWorkspace)
	if err != nil {
		return err
	}
	discoveryCtx, cancelDiscovery := context.WithTimeout(context.Background(), 5*time.Second)
	discoveredModels, discoveryErr := toolconfig.DiscoverCodexModels(discoveryCtx, "codex")
	cancelDiscovery()
	if discoveryErr == nil {
		toolConfig = toolconfig.ApplyCodexModels(toolConfig, discoveredModels)
	} else {
		slog.Debug("using configured Codex models", "error", discoveryErr)
	}
	modelConfigMu := sync.Mutex{}
	if *runtimeFile == "" {
		*runtimeFile = filepath.Join(*dataDir, "runtime.json")
	}
	if *authToken == "" {
		*authToken, err = runtimeinfo.GenerateToken()
		if err != nil {
			return err
		}
	}

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	broker := eventbroker.New()
	store, err := storesqlite.Open(
		context.Background(),
		filepath.Join(*dataDir, "manager.db"),
		storesqlite.WithEventPublisher(broker),
	)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	// Remove empty sessions (no runs and no events) created previously by the manager.
	deleted, err := store.DeleteEmptySessions(context.Background())
	if err != nil {
		return fmt.Errorf("delete empty sessions: %w", err)
	}
	if deleted > 0 {
		slog.Info("removed empty sessions", "count", deleted)
	}
	now := time.Now().UTC()
	if expired, expireErr := store.ExpirePendingApprovals(context.Background(), now); expireErr != nil {
		return fmt.Errorf("expire interrupted approvals: %w", expireErr)
	} else if expired > 0 {
		slog.Info("expired interrupted approvals", "count", expired)
	}
	if interrupted, interruptErr := store.FailInterruptedRuns(context.Background(), now); interruptErr != nil {
		return fmt.Errorf("fail interrupted runs: %w", interruptErr)
	} else if interrupted > 0 {
		slog.Info("failed interrupted runs", "count", interrupted)
	}
	defer store.Close()
	pricingModels := make(map[string][]string)
	fallbackModels := make(map[string]string)
	for _, provider := range toolConfig.Providers {
		pricingModels[string(provider.ID)] = provider.Models
		fallback := provider.DefaultModel
		if fallback == "" && len(provider.Models) > 0 {
			fallback = provider.Models[0]
		}
		if fallback != "auto" {
			fallbackModels[string(provider.ID)] = fallback
		}
	}
	pricingCtx, cancelPricing := context.WithTimeout(context.Background(), 10*time.Second)
	fetcher := pricing.Fetcher{}
	for _, value := range fetcher.Refresh(pricingCtx, pricingModels) {
		_ = store.UpsertModelPricing(pricingCtx, value)
	}
	if backfilled, backfillErr := store.BackfillRunCosts(pricingCtx, fallbackModels); backfillErr != nil {
		slog.Warn("historical run cost backfill failed", "error", backfillErr)
	} else if backfilled > 0 {
		slog.Info("backfilled historical run costs", "count", backfilled)
	}
	cancelPricing()
	if *backfillCosts {
		return nil
	}
	checkpointManager, err := checkpoint.New()
	if err != nil {
		return err
	}
	sessions := sessionservice.New(store, checkpointManager, sessionservice.WithSourceStatsAnalyzer(sourcestats.New("cloc")))
	changeDetector, err := changeset.New()
	if err != nil {
		return err
	}
	approvalRules := make([][]string, 0, len(toolConfig.CommandApproval.AllowedCommands))
	for _, rule := range toolConfig.CommandApproval.AllowedCommands {
		approvalRules = append(approvalRules, append([]string(nil), rule.Argv...))
	}
	var reviewer approvalservice.Reviewer
	if model := toolConfig.CommandApproval.Reviewer.ModelByProvider[string(protocol.AgentCodex)]; model != "" {
		reviewer = approvalservice.CodexReviewer{Binary: "codex", Model: model}
	}
	approvals := approvalservice.New(store, approvalservice.Config{
		Enabled: toolConfig.CommandApproval.Enabled, MaxRisk: toolConfig.CommandApproval.AutoApproveMaxRisk,
		MinConfidence:   toolConfig.CommandApproval.Reviewer.MinConfidence,
		HumanTimeout:    time.Duration(toolConfig.CommandApproval.HumanResponseTimeoutSeconds) * time.Second,
		ReviewerTimeout: time.Duration(toolConfig.CommandApproval.Reviewer.TimeoutSeconds) * time.Second,
		AllowedCommands: approvalRules, Reviewer: reviewer,
		SavePermanentRule: func(argv []string) error {
			modelConfigMu.Lock()
			defer modelConfigMu.Unlock()
			return toolconfig.SaveAllowedCommand(resolvedConfigPath, &toolConfig, argv)
		},
	})
	defer approvals.Close()
	runs := runservice.NewMulti(store, []agent.Adapter{codex.New("codex"), claude.New("claude"), copilot.New("copilot")}, runservice.WithCheckpointManager(checkpointManager), runservice.WithChangeDetector(changeDetector), runservice.WithPricingReader(store), runservice.WithApprovalHandler(approvals.Handle))
	restores, err := restoreservice.New(store, checkpointManager)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = runs.Close(closeCtx)
	}()

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	if err := runtimeinfo.Write(*runtimeFile, runtimeinfo.Metadata{
		PID:           os.Getpid(),
		Address:       listener.Addr().String(),
		AuthToken:     *authToken,
		Version:       version,
		SchemaVersion: protocol.SchemaVersion,
		StartedAt:     time.Now().UTC(),
	}); err != nil {
		return err
	}
	defer os.Remove(*runtimeFile)

	httpServer := &http.Server{
		Handler: server.New(server.Config{
			Version:           version,
			SchemaVersion:     protocol.SchemaVersion,
			DefaultWorkspace:  defaultWorkspace,
			AuthToken:         *authToken,
			AllowedOrigins:    splitNonEmpty(*allowedOrigins),
			EventSubscriber:   broker,
			SessionCreator:    sessions,
			SessionCloser:     sessions,
			RunController:     runs,
			ChangeReader:      store,
			RestoreController: restores,
			Providers:         toolConfig.Providers,
			ModelSetter: func(_ context.Context, provider protocol.AgentName, model string) error {
				modelConfigMu.Lock()
				defer modelConfigMu.Unlock()
				return toolconfig.SaveDefaultModel(resolvedConfigPath, &toolConfig, provider, model)
			},
			UsageReader:        store,
			SourceStatsReader:  store,
			ApprovalController: approvals,
		}, store, store).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("agent manager listening", "address", listener.Addr().String(), "version", version, "runtime_file", *runtimeFile, "config_file", resolvedConfigPath)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runs.Close(shutdownCtx); err != nil {
			return fmt.Errorf("stop active runs: %w", err)
		}
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func defaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user data directory: %w", err)
	}
	return filepath.Join(base, "maatgen"), nil
}
