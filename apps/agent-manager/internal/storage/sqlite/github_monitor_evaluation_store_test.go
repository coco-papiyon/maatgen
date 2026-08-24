package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func TestApplyItemObservationPersistsBaselineAndEventsAtomically(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	observed := testObservedItem(monitor.Repository, 1, createdAt)
	event := testMonitorEvent("event-1", monitor.Repository, createdAt)

	inserted, err := store.ApplyItemObservation(ctx, observed, []protocol.GitHubMonitorEvent{event})
	if err != nil {
		t.Fatalf("ApplyItemObservation: %v", err)
	}
	if len(inserted) != 1 || !inserted[0] {
		t.Fatalf("inserted = %#v, want [true]", inserted)
	}

	if _, err := store.GetObservedItem(ctx, monitor.Repository, protocol.GitHubItemIssue, 1); err != nil {
		t.Fatalf("observed item should have been persisted: %v", err)
	}
	if _, err := store.GetMonitorEvent(ctx, event.ID); err != nil {
		t.Fatalf("event should have been persisted: %v", err)
	}
}

func TestApplyItemObservationDedupesWithinSameBatch(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	observed := testObservedItem(monitor.Repository, 1, createdAt)
	first := testMonitorEvent("event-1", monitor.Repository, createdAt)
	second := testMonitorEvent("event-2", monitor.Repository, createdAt)
	second.DeliveryKey = first.DeliveryKey // two rules computed to the same key

	inserted, err := store.ApplyItemObservation(ctx, observed, []protocol.GitHubMonitorEvent{first, second})
	if err != nil {
		t.Fatalf("ApplyItemObservation: %v", err)
	}
	if len(inserted) != 2 || !inserted[0] || inserted[1] {
		t.Fatalf("inserted = %#v, want [true, false]", inserted)
	}
	if _, err := store.GetMonitorEvent(ctx, "event-1"); err != nil {
		t.Fatalf("event-1 should exist: %v", err)
	}
	if _, err := store.GetMonitorEvent(ctx, "event-2"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("event-2 should have been deduplicated, err = %v", err)
	}
	// The observed baseline must still be persisted even though one of the
	// two candidate events in the batch was a duplicate: a dedup hit must
	// never abort the whole transaction.
	if _, err := store.GetObservedItem(ctx, monitor.Repository, protocol.GitHubItemIssue, 1); err != nil {
		t.Fatalf("observed item should still be persisted: %v", err)
	}
}

func TestApplyItemObservationDedupesAcrossCalls(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	observed := testObservedItem(monitor.Repository, 1, createdAt)
	event := testMonitorEvent("event-1", monitor.Repository, createdAt)
	if inserted, err := store.ApplyItemObservation(ctx, observed, []protocol.GitHubMonitorEvent{event}); err != nil || !inserted[0] {
		t.Fatalf("first call: inserted=%v err=%v", inserted, err)
	}

	replay := testMonitorEvent("event-1-again", monitor.Repository, createdAt.Add(time.Minute))
	replay.DeliveryKey = event.DeliveryKey
	inserted, err := store.ApplyItemObservation(ctx, observed, []protocol.GitHubMonitorEvent{replay})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(inserted) != 1 || inserted[0] {
		t.Fatalf("inserted = %#v, want [false]: same delivery key as the first call", inserted)
	}
	if _, err := store.GetMonitorEvent(ctx, "event-1-again"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("event-1-again should not exist, err = %v", err)
	}
}

func TestApplyItemObservationWithNoEvents(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	observed := testObservedItem(monitor.Repository, 1, createdAt)
	inserted, err := store.ApplyItemObservation(ctx, observed, nil)
	if err != nil {
		t.Fatalf("ApplyItemObservation: %v", err)
	}
	if len(inserted) != 0 {
		t.Fatalf("inserted = %#v, want empty", inserted)
	}
	if _, err := store.GetObservedItem(ctx, monitor.Repository, protocol.GitHubItemIssue, 1); err != nil {
		t.Fatalf("observed item should still be persisted with no events: %v", err)
	}
}
