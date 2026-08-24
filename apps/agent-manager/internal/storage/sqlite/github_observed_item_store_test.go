package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func testObservedItem(repository string, number int, observedAt time.Time) protocol.GitHubObservedItem {
	return protocol.GitHubObservedItem{
		Repository:        repository,
		Kind:              protocol.GitHubItemIssue,
		Number:            number,
		StateHash:         "hash-1",
		LastAction:        "opened",
		ProjectsAvailable: true,
		Snapshot: protocol.GitHubItem{
			Kind:   protocol.GitHubItemIssue,
			Number: number,
			Title:  "Something",
			State:  protocol.GitHubItemOpen,
		},
		FirstSyncedAt: observedAt,
		ObservedAt:    observedAt,
	}
}

func TestObservedItemGetMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	if _, err := store.GetObservedItem(ctx, monitor.Repository, protocol.GitHubItemIssue, 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (first sync baseline case)", err)
	}
}

func TestObservedItemUpsertThenGetAndList(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	item := testObservedItem(monitor.Repository, 1, createdAt)
	if err := store.UpsertObservedItem(ctx, item); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetObservedItem(ctx, monitor.Repository, protocol.GitHubItemIssue, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.StateHash != "hash-1" || got.LastAction != "opened" || !got.ProjectsAvailable {
		t.Fatalf("got = %#v", got)
	}
	if got.Snapshot.Title != "Something" {
		t.Fatalf("snapshot = %#v", got.Snapshot)
	}
	if !got.FirstSyncedAt.Equal(createdAt) || !got.ObservedAt.Equal(createdAt) {
		t.Fatalf("timestamps = %#v", got)
	}

	updatedAt := createdAt.Add(time.Hour)
	item.StateHash = "hash-2"
	item.LastAction = "updated"
	item.Snapshot.Title = "Something else"
	item.ObservedAt = updatedAt
	if err := store.UpsertObservedItem(ctx, item); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	got, err = store.GetObservedItem(ctx, monitor.Repository, protocol.GitHubItemIssue, 1)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.StateHash != "hash-2" || got.LastAction != "updated" || got.Snapshot.Title != "Something else" {
		t.Fatalf("got after update = %#v", got)
	}
	if !got.FirstSyncedAt.Equal(createdAt) {
		t.Fatalf("firstSyncedAt should not change on update, got %v", got.FirstSyncedAt)
	}
	if !got.ObservedAt.Equal(updatedAt) {
		t.Fatalf("observedAt = %v, want %v", got.ObservedAt, updatedAt)
	}

	second := testObservedItem(monitor.Repository, 2, createdAt)
	if err := store.UpsertObservedItem(ctx, second); err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	items, err := store.ListObservedItems(ctx, monitor.Repository)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 || items[0].Number != 1 || items[1].Number != 2 {
		t.Fatalf("items = %#v", items)
	}
}
