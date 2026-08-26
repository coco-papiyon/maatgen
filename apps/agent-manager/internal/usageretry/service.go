// Package usageretry implements ADR-008: when a Run stopped because its
// provider CLI reported a usage/session limit (detected by run.Service, see
// internal/run/service.go), this package waits for the provider's usage to
// recover and then automatically resumes the same Session with one retry
// Run, exactly as a user would resend a follow-up prompt.
package usageretry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
)

// continuationPrompt is sent as the resumed Run's prompt. The Session's
// existing thread already has the interrupted conversation's context (the
// same mechanism a manual follow-up prompt relies on), so this only needs to
// nudge the agent to continue rather than restate the original instruction.
const continuationPrompt = "使用量上限のため中断していた処理を再開してください。中断前の指示の続きを行ってください。"

// defaultPollInterval is long relative to internal/githuboutbox's 5s default
// because provider usage windows recover on the order of hours, not
// seconds.
const defaultPollInterval = 60 * time.Second

// Store is the persistence dependency Service needs. It is satisfied by
// *sqlite.Store.
type Store interface {
	ListRunsPendingUsageLimitRetry(ctx context.Context) ([]protocol.AgentRun, error)
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
	UpdateRun(ctx context.Context, run protocol.AgentRun) error
}

// RunStarter is satisfied by *run.Service.
type RunStarter interface {
	StartRun(ctx context.Context, sessionID string, request protocol.SendMessageRequest) (protocol.AgentRun, error)
}

// ProviderUsageReader is satisfied by *providerusage.Service. This is the
// "usage check" ADR-008 reuses to decide when a pending retry may proceed.
type ProviderUsageReader interface {
	GetProviderUsage(ctx context.Context, provider protocol.AgentName, directory string) (protocol.ProviderUsage, error)
}

// Service is the usage-limit auto-retry poller.
type Service struct {
	store         Store
	runs          RunStarter
	providerUsage ProviderUsageReader
	now           func() time.Time
	pollInterval  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type Option func(*Service)

// WithPollInterval overrides how often Start's background loop calls Tick.
func WithPollInterval(interval time.Duration) Option {
	return func(s *Service) { s.pollInterval = interval }
}

func New(store Store, runs RunStarter, providerUsage ProviderUsageReader, options ...Option) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		store: store, runs: runs, providerUsage: providerUsage,
		now: time.Now, pollInterval: defaultPollInterval,
		ctx: ctx, cancel: cancel,
	}
	for _, option := range options {
		option(s)
	}
	return s
}

// Start launches the background polling loop. Call Close to stop it.
func (s *Service) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := s.Tick(s.ctx); err != nil {
					slog.Error("usage limit retry tick failed", "error", err)
				}
			}
		}
	}()
}

// Close stops the background loop and waits for the in-flight tick, if any,
// to finish.
func (s *Service) Close(ctx context.Context) error {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Tick checks every Run awaiting a usage-limit retry and, for each whose
// provider usage has recovered, starts the resume Run on the same Session.
func (s *Service) Tick(ctx context.Context) error {
	pending, err := s.store.ListRunsPendingUsageLimitRetry(ctx)
	if err != nil {
		return fmt.Errorf("list runs pending usage limit retry: %w", err)
	}
	for _, run := range pending {
		s.retryOne(ctx, run)
	}
	return nil
}

func (s *Service) retryOne(ctx context.Context, run protocol.AgentRun) {
	session, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil {
		slog.Error("usage limit retry: load session failed", "run", run.ID, "session", run.SessionID, "error", err)
		s.clearPending(ctx, run)
		return
	}
	if session.Status != protocol.SessionActive {
		slog.Info("usage limit retry: session is closed, giving up", "run", run.ID, "session", session.ID)
		s.clearPending(ctx, run)
		return
	}
	if !s.isUsageRecovered(ctx, session.Agent, session.Workspace) {
		return // Leave pending; Tick re-checks on the next poll.
	}

	originalRunID := run.ID
	started, err := s.runs.StartRun(ctx, session.ID, protocol.SendMessageRequest{
		Message: continuationPrompt, AutoRetryOfRunID: &originalRunID,
	})
	switch {
	case err == nil:
		slog.Info("usage limit recovered; automatically resumed session", "run", run.ID, "session", session.ID, "retryRun", started.ID)
		s.clearPending(ctx, run)
	case errors.Is(err, runservice.ErrRepositoryBusy):
		// The repository's execution lock is held by another Run; retry once
		// it frees up rather than giving up the one automatic retry.
	case errors.Is(err, runservice.ErrRunActive):
		// The session already has a newer Run (e.g. the user retried
		// manually while this one waited); nothing left to resume.
		s.clearPending(ctx, run)
	default:
		slog.Error("usage limit retry: start run failed", "run", run.ID, "session", session.ID, "error", err)
		s.clearPending(ctx, run)
	}
}

// isUsageRecovered reports whether every usage window the provider reports
// has remaining quota. A fetch failure is treated as recovered, consistent
// with internal/githuboutbox's provider usage check: usage monitoring is
// optional, and ADR-008 bounds the consequence of a wrong guess to the one
// automatic retry this Run is already entitled to.
func (s *Service) isUsageRecovered(ctx context.Context, provider protocol.AgentName, directory string) bool {
	usage, err := s.providerUsage.GetProviderUsage(ctx, provider, directory)
	if err != nil {
		return true
	}
	for _, window := range usage.Windows {
		if window.RemainingPercent <= 0 {
			return false
		}
	}
	return true
}

func (s *Service) clearPending(ctx context.Context, run protocol.AgentRun) {
	run.UsageLimitRetryPendingAt = nil
	if err := s.store.UpdateRun(ctx, run); err != nil {
		slog.Error("usage limit retry: clear pending flag failed", "run", run.ID, "error", err)
	}
}
