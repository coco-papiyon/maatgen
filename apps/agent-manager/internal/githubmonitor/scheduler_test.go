package githubmonitor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type fakeSchedulerStore struct {
	monitors []protocol.GitHubRepositoryMonitor
	err      error
}

func (f *fakeSchedulerStore) ListRepositoryMonitors(ctx context.Context) ([]protocol.GitHubRepositoryMonitor, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.monitors, nil
}

type fakeSchedulerPoller struct {
	mu     sync.Mutex
	synced []string
	err    error
}

func (f *fakeSchedulerPoller) SyncRepository(ctx context.Context, repository string) (SyncResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.synced = append(f.synced, repository)
	if f.err != nil {
		return SyncResult{}, f.err
	}
	return SyncResult{}, nil
}

func (f *fakeSchedulerPoller) syncedRepositories() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.synced...)
}

func monitorAt(repository string, enabled bool, nextSyncAt *time.Time) protocol.GitHubRepositoryMonitor {
	return protocol.GitHubRepositoryMonitor{Repository: repository, Enabled: enabled, NextSyncAt: nextSyncAt}
}

func TestSchedulerTickSyncsDueAndNeverSyncedMonitors(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	store := &fakeSchedulerStore{monitors: []protocol.GitHubRepositoryMonitor{
		monitorAt("/repo-due", true, &past),
		monitorAt("/repo-never-synced", true, nil),
	}}
	poller := &fakeSchedulerPoller{}
	scheduler := NewScheduler(store, poller)

	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	synced := poller.syncedRepositories()
	if len(synced) != 2 {
		t.Fatalf("synced = %#v, want both monitors", synced)
	}
}

func TestSchedulerTickSkipsDisabledAndNotYetDueMonitors(t *testing.T) {
	future := time.Now().Add(time.Hour)
	store := &fakeSchedulerStore{monitors: []protocol.GitHubRepositoryMonitor{
		monitorAt("/repo-disabled", false, nil),
		monitorAt("/repo-not-due", true, &future),
	}}
	poller := &fakeSchedulerPoller{}
	scheduler := NewScheduler(store, poller)

	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if synced := poller.syncedRepositories(); len(synced) != 0 {
		t.Fatalf("synced = %#v, want none", synced)
	}
}

func TestSchedulerTickContinuesAfterOneMonitorFails(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	store := &fakeSchedulerStore{monitors: []protocol.GitHubRepositoryMonitor{
		monitorAt("/repo-a", true, &past),
		monitorAt("/repo-b", true, &past),
	}}
	poller := &fakeSchedulerPoller{err: errors.New("rate limited")}
	scheduler := NewScheduler(store, poller)

	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatalf("Tick must not fail just because a sync failed: %v", err)
	}
	if synced := poller.syncedRepositories(); len(synced) != 2 {
		t.Fatalf("synced = %#v, want both attempted despite failures", synced)
	}
}

func TestSchedulerTickPropagatesListError(t *testing.T) {
	store := &fakeSchedulerStore{err: errors.New("database is locked")}
	scheduler := NewScheduler(store, &fakeSchedulerPoller{})
	if err := scheduler.Tick(context.Background()); err == nil {
		t.Fatalf("expected an error")
	}
}

func TestSchedulerStartAndCloseRunsTicksInBackground(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	store := &fakeSchedulerStore{monitors: []protocol.GitHubRepositoryMonitor{monitorAt("/repo", true, &past)}}
	poller := &fakeSchedulerPoller{}
	scheduler := NewScheduler(store, poller, WithSchedulerInterval(10*time.Millisecond))

	scheduler.Start()
	deadline := time.Now().Add(2 * time.Second)
	for len(poller.syncedRepositories()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(poller.syncedRepositories()) == 0 {
		t.Fatalf("expected at least one background tick to run")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := scheduler.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
