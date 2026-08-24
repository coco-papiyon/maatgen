package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testMonitor(repository string, createdAt time.Time) protocol.GitHubRepositoryMonitor {
	return protocol.GitHubRepositoryMonitor{
		Repository:          repository,
		Host:                "github.com",
		Owner:               "octo-org",
		Name:                "example",
		RemoteName:          "origin",
		Enabled:             true,
		PollIntervalSeconds: 300,
		CoalesceQueueLimit:  20,
		CreatedAt:           createdAt,
		UpdatedAt:           createdAt,
	}
}

func TestRepositoryMonitorCreateGetList(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.CreateRepositoryMonitor(ctx, monitor); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate create err = %v, want ErrConflict", err)
	}

	got, err := store.GetRepositoryMonitor(ctx, monitor.Repository)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Host != "github.com" || got.Owner != "octo-org" || !got.Enabled || got.PollIntervalSeconds != 300 {
		t.Fatalf("got = %#v", got)
	}
	if !got.CreatedAt.Equal(createdAt) || !got.UpdatedAt.Equal(createdAt) {
		t.Fatalf("timestamps = %#v", got)
	}
	if got.LastSyncedAt != nil || got.NextSyncAt != nil || got.LastError != nil {
		t.Fatalf("expected nil sync state initially, got %#v", got)
	}

	second := testMonitor("C:/workspace/other", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, second); err != nil {
		t.Fatalf("create second: %v", err)
	}
	list, err := store.ListRepositoryMonitors(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Repository != "C:/workspace/example" || list[1].Repository != "C:/workspace/other" {
		t.Fatalf("list = %#v, want alphabetical by repository", list)
	}
}

func TestRepositoryMonitorGetMissingReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.GetRepositoryMonitor(context.Background(), "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRepositoryMonitorUpdateSettings(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create: %v", err)
	}

	monitor.Enabled = false
	monitor.PollIntervalSeconds = 600
	monitor.CoalesceQueueLimit = 5
	monitor.RemoteName = "upstream"
	updatedAt := createdAt.Add(time.Hour)
	if err := store.UpdateRepositoryMonitorSettings(ctx, monitor, updatedAt); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	got, err := store.GetRepositoryMonitor(ctx, monitor.Repository)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Enabled || got.PollIntervalSeconds != 600 || got.CoalesceQueueLimit != 5 || got.RemoteName != "upstream" {
		t.Fatalf("got = %#v", got)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updatedAt = %v, want %v", got.UpdatedAt, updatedAt)
	}
}

func TestRepositoryMonitorUpdateSettingsMissingReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	monitor := testMonitor("missing", time.Now().UTC())
	if err := store.UpdateRepositoryMonitorSettings(context.Background(), monitor, time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRepositoryMonitorUpdateSyncState(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create: %v", err)
	}

	syncedAt := createdAt.Add(5 * time.Minute)
	nextAt := syncedAt.Add(5 * time.Minute)
	if err := store.UpdateRepositoryMonitorSyncState(ctx, monitor.Repository, syncedAt, nextAt, nil, syncedAt); err != nil {
		t.Fatalf("update sync state: %v", err)
	}
	got, err := store.GetRepositoryMonitor(ctx, monitor.Repository)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(syncedAt) {
		t.Fatalf("lastSyncedAt = %#v", got.LastSyncedAt)
	}
	if got.NextSyncAt == nil || !got.NextSyncAt.Equal(nextAt) {
		t.Fatalf("nextSyncAt = %#v", got.NextSyncAt)
	}
	if got.LastError != nil {
		t.Fatalf("lastError = %#v, want nil", got.LastError)
	}

	failedAt := nextAt.Add(5 * time.Minute)
	errMessage := "rate limited"
	if err := store.UpdateRepositoryMonitorSyncState(ctx, monitor.Repository, failedAt, failedAt.Add(time.Minute), &errMessage, failedAt); err != nil {
		t.Fatalf("update sync state with error: %v", err)
	}
	got, err = store.GetRepositoryMonitor(ctx, monitor.Repository)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LastError == nil || *got.LastError != errMessage {
		t.Fatalf("lastError = %#v", got.LastError)
	}
}

func TestApplyRemoteChangeUpdatesTargetClearsObservationsAndResetsSyncState(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create: %v", err)
	}

	syncedAt := createdAt.Add(5 * time.Minute)
	nextAt := syncedAt.Add(5 * time.Minute)
	if err := store.UpdateRepositoryMonitorSyncState(ctx, monitor.Repository, syncedAt, nextAt, nil, syncedAt); err != nil {
		t.Fatalf("seed sync state: %v", err)
	}
	observed := protocol.GitHubObservedItem{
		Repository: monitor.Repository, Kind: protocol.GitHubItemIssue, Number: 1,
		StateHash: "hash-1", LastAction: "opened",
		Snapshot:      protocol.GitHubItem{Kind: protocol.GitHubItemIssue, Number: 1},
		FirstSyncedAt: syncedAt, ObservedAt: syncedAt,
	}
	if err := store.UpsertObservedItem(ctx, observed); err != nil {
		t.Fatalf("seed observed item: %v", err)
	}

	changed := monitor
	changed.Host, changed.Owner, changed.Name, changed.RemoteName = "github.com", "new-org", "new-repo", "upstream"
	changed.LastSyncedAt, changed.NextSyncAt = nil, nil
	appliedAt := nextAt.Add(time.Hour)
	if err := store.ApplyRemoteChange(ctx, changed, appliedAt); err != nil {
		t.Fatalf("apply remote change: %v", err)
	}

	got, err := store.GetRepositoryMonitor(ctx, monitor.Repository)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Host != "github.com" || got.Owner != "new-org" || got.Name != "new-repo" || got.RemoteName != "upstream" {
		t.Fatalf("got = %#v, want new remote target", got)
	}
	if got.LastSyncedAt != nil || got.NextSyncAt != nil {
		t.Fatalf("got sync state = %#v, want both nil after remote change", got)
	}
	if !got.UpdatedAt.Equal(appliedAt) {
		t.Fatalf("updatedAt = %v, want %v", got.UpdatedAt, appliedAt)
	}

	if _, err := store.GetObservedItem(ctx, monitor.Repository, protocol.GitHubItemIssue, 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("observed item after remote change err = %v, want ErrNotFound", err)
	}
}

func TestApplyRemoteChangeMissingReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	monitor := testMonitor("missing", time.Now().UTC())
	if err := store.ApplyRemoteChange(context.Background(), monitor, time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRepositoryMonitorDeleteCascades(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create: %v", err)
	}
	rule := testTriggerRule("rule-1", monitor.Repository, createdAt)
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	if err := store.DeleteRepositoryMonitor(ctx, monitor.Repository); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetRepositoryMonitor(ctx, monitor.Repository); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
	if _, err := store.GetTriggerRule(ctx, rule.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("rule after cascade delete err = %v, want ErrNotFound", err)
	}
}

func TestRepositoryMonitorDeleteMissingReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	if err := store.DeleteRepositoryMonitor(context.Background(), "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
