package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
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
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubapi"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubcontroller"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubmonitor"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githuboutbox"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/gitworktree"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/pricing"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/providerusage"
	restoreservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/restore"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/runtimeinfo"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/server"
	sessionservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/sourcestats"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/toolconfig"
)

var version = "dev"

// sessionAutoCloseAge is how long a session may stay active before it is
// closed automatically at the next manager startup.
const sessionAutoCloseAge = 24 * time.Hour

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
	allowedOrigins := flag.String("allowed-origins", "http://localhost:5173,http://127.0.0.1:5173", "comma-separated browser origins")
	configFile := flag.String("config", toolconfig.DefaultRelativePath, "tool configuration path relative to the executable")
	staticDir := flag.String("static-dir", "", "directory of built Web UI static assets to serve; auto-detected when omitted")
	backfillCosts := flag.Bool("backfill-costs", false, "refresh pricing and recalculate historical run costs, then exit")
	flag.Parse()
	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	resolvedStaticDir := resolveStaticDir(*staticDir, executablePath, defaultWorkspace)
	var staticFS fs.FS
	if resolvedStaticDir != "" {
		staticFS = os.DirFS(resolvedStaticDir)
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
	codexAdapter := codex.New("codex")
	claudeAdapter := claude.New("claude")
	copilotAdapter := copilot.New("copilot")
	adapters := []agent.Adapter{codexAdapter, claudeAdapter, copilotAdapter}
	availabilityCtx, cancelAvailability := context.WithTimeout(context.Background(), 5*time.Second)
	availableProviders := agent.AvailableProviders(availabilityCtx, adapters)
	cancelAvailability()
	for name, available := range availableProviders {
		if !available {
			slog.Info("agent CLI is not installed; hiding it from the provider list", "provider", name)
		}
	}
	modelConfigMu := sync.Mutex{}
	if *runtimeFile == "" {
		*runtimeFile = filepath.Join(*dataDir, "runtime.json")
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
	for _, value := range pricing.Static() {
		_ = store.UpsertModelPricing(pricingCtx, value)
	}
	for i := range toolConfig.Providers {
		for _, model := range toolConfig.Providers[i].Models {
			p, pErr := store.GetModelPricing(pricingCtx, string(toolConfig.Providers[i].ID), model)
			if pErr != nil {
				continue
			}
			if toolConfig.Providers[i].Pricing == nil {
				toolConfig.Providers[i].Pricing = make(map[string]*protocol.ModelPricingInfo)
			}
			toolConfig.Providers[i].Pricing[model] = &protocol.ModelPricingInfo{
				InputPerMillion:  p.InputPerMillion,
				OutputPerMillion: p.OutputPerMillion,
			}
		}
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
	if closedCount, closeErr := closeExpiredSessions(context.Background(), store, sessions, now, sessionAutoCloseAge); closeErr != nil {
		return fmt.Errorf("close expired sessions: %w", closeErr)
	} else if closedCount > 0 {
		slog.Info("closed expired sessions", "count", closedCount, "max_age", sessionAutoCloseAge)
	}
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
	// dispatcher and githubMonitor are assigned below, once runs exists; the
	// terminal observer closure only runs asynchronously (after a Run
	// finishes), by which time both are always set.
	var dispatcher *githuboutbox.Dispatcher
	var githubMonitor *githubcontroller.Service
	runs := runservice.NewMulti(store, adapters, runservice.WithCheckpointManager(checkpointManager), runservice.WithChangeDetector(changeDetector), runservice.WithPricingReader(store), runservice.WithApprovalHandler(approvals.Handle),
		runservice.WithTerminalObserver(func(run protocol.AgentRun, workspace string) {
			dispatcher.ObserveRunTerminal(run)
			// Issue #15: trigger the same GitHub monitor sync the Settings
			// screen's manual "今すぐ同期" button performs, so a rule matching
			// what this Run just changed (e.g. a newly opened PR awaiting
			// review) does not have to wait for the next scheduled poll. Run in
			// the background so the GitHub API round-trip never delays
			// releasing this repository's execution lock (done right after
			// this observer returns).
			if githubMonitor == nil {
				return
			}
			go func() {
				syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if _, err := githubMonitor.SyncNow(syncCtx, workspace); err != nil && !errors.Is(err, storage.ErrNotFound) {
					slog.Warn("github monitor sync after run completion failed", "workspace", workspace, "error", err)
				}
			}()
		}))
	providerUsage := providerusage.New(adapters)
	restores, err := restoreservice.New(store, checkpointManager)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = runs.Close(closeCtx)
	}()

	// GitHub monitoring (ADR-007): the Outbox dispatcher turns queued
	// monitor events into ordinary Sessions/Runs through the same sessions
	// and runs services constructed above, so an automated Run is
	// indistinguishable from a manual one downstream.
	dispatcher = githuboutbox.NewDispatcher(store, sessions, runs, githuboutbox.WithProviderUsage(providerUsage))
	if reconcileErr := dispatcher.Reconcile(context.Background()); reconcileErr != nil {
		slog.Warn("github monitor outbox reconciliation failed", "error", reconcileErr)
	}
	dispatcher.Start()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = dispatcher.Close(closeCtx)
	}()

	configuredGitHubToken := toolConfig.GitHub.Token
	githubClients := func(host string) (*githubapi.Client, error) {
		token := configuredGitHubToken
		if token == "" {
			// Read gh's credential for each client creation so a user can run
			// `gh auth login` while the manager is already running. If it is
			// unavailable, NewClient returns a typed authentication error that
			// the Web API turns into an actionable message.
			discovered, discoverErr := toolconfig.DiscoverGitHubToken(context.Background(), host)
			if discoverErr != nil {
				slog.Warn("github cli authentication unavailable", "error", discoverErr)
			} else {
				token = discovered
			}
		}
		return githubapi.NewClient(host, token)
	}
	evaluator := githubmonitor.NewEvaluator(store)
	remoteResolver := func(ctx context.Context, repository string, selectedRemoteName string) (*githubmonitor.RemoteCandidate, error) {
		resolution, err := gitworktree.ResolveGitHubRemote(ctx, checkpointManager.GitPath(), repository, toolConfig.GitHub.AllowedEnterpriseHosts)
		if err != nil && !errors.Is(err, gitworktree.ErrNoGitHubRemote) {
			return nil, err
		}
		var candidate *gitworktree.GitHubRepository
		if selectedRemoteName != "" {
			for _, c := range resolution.Candidates {
				if c.RemoteName == selectedRemoteName {
					candidate = &c
					break
				}
			}
		} else {
			candidate = resolution.Repository
		}
		if candidate == nil {
			return nil, nil
		}
		return &githubmonitor.RemoteCandidate{Host: candidate.Host, Owner: candidate.Owner, Name: candidate.Name, RemoteName: candidate.RemoteName}, nil
	}
	poller := githubmonitor.NewPoller(store, evaluator, func(host string) (githubmonitor.GitHubClient, error) {
		return githubClients(host)
	}, remoteResolver)
	githubMonitor = githubcontroller.New(store, checkpointManager, checkpointManager.GitPath(), toolConfig.GitHub.AllowedEnterpriseHosts, poller,
		func(host string) (githubcontroller.GitHubClient, error) { return githubClients(host) })

	// Periodic polling (ADR-007 section 2): separate from the manual "sync
	// now" action (which calls the same Poller directly through
	// githubMonitor.SyncNow), this makes each enabled repository monitor
	// sync automatically on its own configured interval.
	githubScheduler := githubmonitor.NewScheduler(store, poller)
	githubScheduler.Start()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = githubScheduler.Close(closeCtx)
	}()

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	if err := runtimeinfo.Write(*runtimeFile, runtimeinfo.Metadata{
		PID:           os.Getpid(),
		Address:       listener.Addr().String(),
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
			AllowedOrigins:    splitNonEmpty(*allowedOrigins),
			EventSubscriber:   broker,
			SessionCreator:    sessions,
			SessionCloser:     sessions,
			SessionReopener:   sessions,
			RunController:     runs,
			ChangeReader:      store,
			RestoreController: restores,
			Providers:         filterAvailableProviders(toolConfig.Providers, availableProviders),
			ModelSetter: func(_ context.Context, provider protocol.AgentName, model string) error {
				modelConfigMu.Lock()
				defer modelConfigMu.Unlock()
				return toolconfig.SaveDefaultModel(resolvedConfigPath, &toolConfig, provider, model)
			},
			UsageReader:             store,
			ProviderUsageReader:     providerUsage,
			UsageSummaryReader:      store,
			SourceStatsReader:       store,
			ApprovalController:      approvals,
			WorkspaceReader:         sessions,
			GitHubMonitorController: githubMonitor,
			StaticFS:                staticFS,
		}, store, store).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	if resolvedStaticDir == "" {
		slog.Info("no static Web UI assets found; serving API only", "static_dir", *staticDir)
	}
	slog.Info("agent manager listening", "address", listener.Addr().String(), "version", version, "runtime_file", *runtimeFile, "config_file", resolvedConfigPath, "static_dir", resolvedStaticDir)

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

// closeExpiredSessions closes active sessions created more than maxAge ago.
// It runs once at startup, so a session left open across manager restarts
// stops appearing in the active list and its checkpoint working copy gets
// cleaned up the same way a manual close would. Sessions that still have an
// active run are left alone; a future startup will retry them.
func closeExpiredSessions(ctx context.Context, store *storesqlite.Store, sessions *sessionservice.Service, now time.Time, maxAge time.Duration) (int, error) {
	cutoff := now.Add(-maxAge)
	closed := 0
	var cursor *protocol.SessionCursor
	for {
		page, err := store.ListSessions(ctx, 100, cursor, protocol.SessionActive)
		if err != nil {
			return closed, fmt.Errorf("list active sessions: %w", err)
		}
		if len(page) == 0 {
			return closed, nil
		}
		for _, item := range page {
			if !item.CreatedAt.Before(cutoff) {
				continue
			}
			if _, closeErr := sessions.CloseSession(ctx, item.ID); closeErr != nil {
				if errors.Is(closeErr, sessionservice.ErrRunActive) {
					slog.Warn("skipped closing expired session with an active run", "session", item.ID)
					continue
				}
				slog.Warn("failed to close expired session", "session", item.ID, "error", closeErr)
				continue
			}
			closed++
		}
		if len(page) < 100 {
			return closed, nil
		}
		last := page[len(page)-1]
		cursor = &protocol.SessionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
}

// filterAvailableProviders hides providers whose CLI was not found at
// startup from the API response. The full, unfiltered list in toolConfig
// itself is left untouched so config persistence (default model, allowed
// commands) never drops a provider just because its CLI is missing right now.
func filterAvailableProviders(providers []protocol.Provider, available map[protocol.AgentName]bool) []protocol.Provider {
	filtered := make([]protocol.Provider, 0, len(providers))
	for _, provider := range providers {
		if available[provider.ID] {
			filtered = append(filtered, provider)
		}
	}
	return filtered
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

// resolveStaticDir locates the built Web UI static assets. When configured
// explicitly the directory must exist. Otherwise it checks the packaged
// production layout (a "web/dist" directory next to the executable) and the
// monorepo development layout (apps/web/dist as a sibling of the
// agent-manager working directory used by `go run`). It returns "" when no
// assets are found, in which case the server runs API-only.
func resolveStaticDir(configured, executablePath, workingDirectory string) string {
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && info.IsDir() {
			return configured
		}
		return ""
	}
	candidates := []string{
		filepath.Join(filepath.Dir(executablePath), "web", "dist"),
		filepath.Join(workingDirectory, "..", "web", "dist"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func defaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user data directory: %w", err)
	}
	return filepath.Join(base, "maatgen"), nil
}
