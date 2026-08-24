package githubmonitor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// defaultSchedulerInterval is how often Scheduler checks which repository
// monitors are due, independent of any single monitor's own
// PollIntervalSeconds. It only needs to be small relative to the shortest
// configured poll interval so a due monitor isn't kept waiting long past
// its schedule.
const defaultSchedulerInterval = 30 * time.Second

// SchedulerStore is the persistence dependency Scheduler needs beyond what
// Poller already requires.
type SchedulerStore interface {
	ListRepositoryMonitors(ctx context.Context) ([]protocol.GitHubRepositoryMonitor, error)
}

// SchedulerPoller runs one sync cycle for a repository. It is satisfied by
// *Poller; declared as an interface here purely for testability.
type SchedulerPoller interface {
	SyncRepository(ctx context.Context, repository string) (SyncResult, error)
}

// Scheduler is the periodic-polling half of ADR-007 section 2: "Agent
// Managerが設定された間隔でGitHub APIをポーリングする". It is separate
// from a manual "sync now" action (githubcontroller.Service.SyncNow),
// which calls the same Poller directly; Scheduler is what makes polling
// happen automatically, on each monitor's own configured cadence, without
// any user action.
type Scheduler struct {
	store    SchedulerStore
	poller   SchedulerPoller
	interval time.Duration
	now      func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type SchedulerOption func(*Scheduler)

// WithSchedulerInterval overrides how often the background loop checks for
// due monitors (not to be confused with a monitor's own
// PollIntervalSeconds, which is compared against NextSyncAt on each check).
func WithSchedulerInterval(interval time.Duration) SchedulerOption {
	return func(s *Scheduler) { s.interval = interval }
}

func NewScheduler(store SchedulerStore, poller SchedulerPoller, options ...SchedulerOption) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	scheduler := &Scheduler{
		store: store, poller: poller, interval: defaultSchedulerInterval, now: time.Now,
		ctx: ctx, cancel: cancel,
	}
	for _, option := range options {
		option(scheduler)
	}
	return scheduler
}

// Start launches the background polling loop. Call Close to stop it.
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := s.Tick(s.ctx); err != nil {
					slog.Error("github monitor scheduler tick failed", "error", err)
				}
			}
		}
	}()
}

// Close stops the background loop and waits for the in-flight tick, if any,
// to finish.
func (s *Scheduler) Close(ctx context.Context) error {
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

// Tick syncs every enabled repository monitor whose NextSyncAt has arrived.
// A monitor with NextSyncAt unset (never synced) is always due. Monitors
// are synced one at a time — the store's single-writer SQLite connection
// makes concurrent syncs pointless work, not a speedup — and one
// monitor's failure (recorded on it by Poller) never stops the rest from
// being checked.
//
// Before syncing, it re-resolves the monitor's remote to detect if the
// repository has moved or the remote URL has changed (ADR-007 section 1:
// "remote変更検出後は前のrepositoryへ問い合わせない").
func (s *Scheduler) Tick(ctx context.Context) error {
	monitors, err := s.store.ListRepositoryMonitors(ctx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, monitor := range monitors {
		if !monitor.Enabled {
			continue
		}
		if monitor.NextSyncAt != nil && monitor.NextSyncAt.After(now) {
			continue
		}
		slog.Info("scheduled github repository poll started",
			"repository", monitor.Repository,
			"github_owner", monitor.Owner,
			"github_repository", monitor.Name,
		)
		result, err := s.poller.SyncRepository(ctx, monitor.Repository)
		if err != nil {
			slog.Warn("scheduled github repository sync failed", "repository", monitor.Repository, "error", err)
			continue
		}
		slog.Info("scheduled github repository poll completed",
			"repository", monitor.Repository,
			"github_owner", monitor.Owner,
			"github_repository", monitor.Name,
			"issues", result.IssuesProcessed,
			"pull_requests", result.PullRequestsProcessed,
			"events_matched", result.EventsMatched,
		)
	}
	return nil
}
