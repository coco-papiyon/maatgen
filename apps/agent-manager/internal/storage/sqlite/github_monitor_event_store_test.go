package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func testMonitorEvent(id, repository string, createdAt time.Time) protocol.GitHubMonitorEvent {
	deliveryKey := "delivery-" + id
	return protocol.GitHubMonitorEvent{
		ID:             id,
		Repository:     repository,
		Kind:           protocol.GitHubItemIssue,
		Number:         1,
		Action:         "opened",
		AfterStateHash: "hash-1",
		DeliveryKey:    &deliveryKey,
		Status:         protocol.GitHubMonitorEventDetected,
		ItemSnapshot: protocol.GitHubItem{
			Kind:   protocol.GitHubItemIssue,
			Number: 1,
			Title:  "Something",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func newTestRepositoryForEvents(t *testing.T, store *Store, createdAt time.Time) protocol.GitHubRepositoryMonitor {
	t.Helper()
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(context.Background(), monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	return monitor
}

func TestInsertMonitorEventDeduplicatesOnDeliveryKey(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	event := testMonitorEvent("event-1", monitor.Repository, createdAt)
	inserted, err := store.InsertMonitorEvent(ctx, event)
	if err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}

	duplicate := testMonitorEvent("event-2", monitor.Repository, createdAt.Add(time.Minute))
	duplicate.DeliveryKey = event.DeliveryKey // same key as event-1
	inserted, err = store.InsertMonitorEvent(ctx, duplicate)
	if err != nil {
		t.Fatalf("duplicate insert returned an error, want silent skip: %v", err)
	}
	if inserted {
		t.Fatalf("duplicate insert reported inserted=true, want false")
	}
	if _, err := store.GetMonitorEvent(ctx, "event-2"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("event-2 should not exist after a deduplicated insert, err = %v", err)
	}
}

func TestInsertMonitorEventDeduplicatesSameRuleAndItemWithDifferentDeliveryKey(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)
	rule := testTriggerRule("rule-1", monitor.Repository, createdAt)
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	first := testMonitorEvent("event-opened", monitor.Repository, createdAt)
	first.RuleID = &rule.ID
	if inserted, err := store.InsertMonitorEvent(ctx, first); err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}

	closed := testMonitorEvent("event-closed", monitor.Repository, createdAt.Add(time.Minute))
	closed.RuleID = &rule.ID
	closed.Action = "closed"
	closed.AfterStateHash = "hash-closed"
	if inserted, err := store.InsertMonitorEvent(ctx, closed); err != nil || inserted {
		t.Fatalf("same rule/item insert: inserted=%v err=%v, want silent deduplication", inserted, err)
	}
	if _, err := store.GetMonitorEvent(ctx, closed.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("closed event should not exist after deduplication, err = %v", err)
	}
}

func TestInsertMonitorEventAllowsMultipleNilDeliveryKeys(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	original := testMonitorEvent("event-0", monitor.Repository, createdAt)
	if inserted, err := store.InsertMonitorEvent(ctx, original); err != nil || !inserted {
		t.Fatalf("insert original: inserted=%v err=%v", inserted, err)
	}

	for _, id := range []string{"replay-1", "replay-2"} {
		event := testMonitorEvent(id, monitor.Repository, createdAt)
		event.DeliveryKey = nil
		event.ReplayOfEventID = strPtr(original.ID)
		inserted, err := store.InsertMonitorEvent(ctx, event)
		if err != nil || !inserted {
			t.Fatalf("insert %s: inserted=%v err=%v", id, inserted, err)
		}
	}
}

func TestMonitorEventLifecycleTransitions(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)
	event := testMonitorEvent("event-1", monitor.Repository, createdAt)
	if inserted, err := store.InsertMonitorEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("insert: inserted=%v err=%v", inserted, err)
	}

	t1 := createdAt.Add(time.Minute)
	if err := store.UpdateMonitorEventStatus(ctx, event.ID, protocol.GitHubMonitorEventMatched, t1); err != nil {
		t.Fatalf("update status matched: %v", err)
	}
	if err := store.UpdateMonitorEventStatus(ctx, event.ID, protocol.GitHubMonitorEventQueued, t1); err != nil {
		t.Fatalf("update status queued: %v", err)
	}

	session := protocol.AgentSession{
		ID: "session-1", Agent: protocol.AgentCodex, Workspace: monitor.Repository,
		Status: protocol.SessionActive, CreatedAt: createdAt,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	t2 := createdAt.Add(2 * time.Minute)
	if err := store.AttachMonitorEventSession(ctx, event.ID, session.ID, t2); err != nil {
		t.Fatalf("attach session: %v", err)
	}
	got, err := store.GetMonitorEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != protocol.GitHubMonitorEventSessionCreated || got.SessionID == nil || *got.SessionID != session.ID {
		t.Fatalf("got = %#v", got)
	}

	run := protocol.AgentRun{ID: "run-1", SessionID: session.ID, Status: protocol.RunQueued, Prompt: "Design it"}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	t3 := createdAt.Add(3 * time.Minute)
	if err := store.AttachMonitorEventRun(ctx, event.ID, run.ID, t3); err != nil {
		t.Fatalf("attach run: %v", err)
	}
	got, err = store.GetMonitorEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != protocol.GitHubMonitorEventRunStarted || got.RunID == nil || *got.RunID != "run-1" {
		t.Fatalf("got = %#v", got)
	}
	if !got.UpdatedAt.Equal(t3) {
		t.Fatalf("updatedAt = %v, want %v", got.UpdatedAt, t3)
	}
}

func TestSkipAndFailMonitorEvent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	skipped := testMonitorEvent("event-skip", monitor.Repository, createdAt)
	if inserted, err := store.InsertMonitorEvent(ctx, skipped); err != nil || !inserted {
		t.Fatalf("insert: inserted=%v err=%v", inserted, err)
	}
	if err := store.SkipMonitorEvent(ctx, skipped.ID, "repository execution lock held", createdAt.Add(time.Minute)); err != nil {
		t.Fatalf("skip: %v", err)
	}
	got, err := store.GetMonitorEvent(ctx, skipped.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != protocol.GitHubMonitorEventSkipped || got.SkipReason == nil || *got.SkipReason != "repository execution lock held" {
		t.Fatalf("got = %#v", got)
	}

	failed := testMonitorEvent("event-fail", monitor.Repository, createdAt)
	if inserted, err := store.InsertMonitorEvent(ctx, failed); err != nil || !inserted {
		t.Fatalf("insert: inserted=%v err=%v", inserted, err)
	}
	if err := store.FailMonitorEvent(ctx, failed.ID, "provider unavailable", createdAt.Add(time.Minute)); err != nil {
		t.Fatalf("fail: %v", err)
	}
	got, err = store.GetMonitorEvent(ctx, failed.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != protocol.GitHubMonitorEventFailed || got.LastError == nil || *got.LastError != "provider unavailable" {
		t.Fatalf("got = %#v", got)
	}
}

func TestCreateReplayEventPreservesOriginalDeliveryKey(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	original := testMonitorEvent("event-1", monitor.Repository, createdAt)
	if inserted, err := store.InsertMonitorEvent(ctx, original); err != nil || !inserted {
		t.Fatalf("insert original: inserted=%v err=%v", inserted, err)
	}
	if err := store.SkipMonitorEvent(ctx, original.ID, "skipped by policy", createdAt.Add(time.Minute)); err != nil {
		t.Fatalf("skip original: %v", err)
	}

	replayedAt := createdAt.Add(time.Hour)
	replay, err := store.CreateReplayEvent(ctx, original.ID, "event-1-replay-1", replayedAt)
	if err != nil {
		t.Fatalf("create replay: %v", err)
	}
	if replay.Status != protocol.GitHubMonitorEventQueued {
		t.Fatalf("replay.Status = %v, want queued", replay.Status)
	}
	if replay.DeliveryKey != nil {
		t.Fatalf("replay.DeliveryKey = %#v, want nil", replay.DeliveryKey)
	}
	if replay.ReplayOfEventID == nil || *replay.ReplayOfEventID != original.ID {
		t.Fatalf("replay.ReplayOfEventID = %#v, want %q", replay.ReplayOfEventID, original.ID)
	}
	if replay.ItemSnapshot.Title != original.ItemSnapshot.Title {
		t.Fatalf("replay.ItemSnapshot = %#v", replay.ItemSnapshot)
	}

	// The original event, and its dedupe key, must be untouched: a second
	// replay of the same original must still succeed.
	originalAfter, err := store.GetMonitorEvent(ctx, original.ID)
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if originalAfter.DeliveryKey == nil || *originalAfter.DeliveryKey != *original.DeliveryKey {
		t.Fatalf("original delivery key changed: %#v", originalAfter.DeliveryKey)
	}
	if originalAfter.Status != protocol.GitHubMonitorEventSkipped {
		t.Fatalf("original status changed to %v, want skipped", originalAfter.Status)
	}

	if _, err := store.CreateReplayEvent(ctx, original.ID, "event-1-replay-2", replayedAt.Add(time.Minute)); err != nil {
		t.Fatalf("create second replay: %v", err)
	}
}

func TestListMonitorEventsOrdersNewestFirstAndRespectsLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	for i, id := range []string{"event-1", "event-2", "event-3"} {
		event := testMonitorEvent(id, monitor.Repository, createdAt.Add(time.Duration(i)*time.Minute))
		if inserted, err := store.InsertMonitorEvent(ctx, event); err != nil || !inserted {
			t.Fatalf("insert %s: inserted=%v err=%v", id, inserted, err)
		}
	}

	events, err := store.ListMonitorEvents(ctx, monitor.Repository, 2, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 || events[0].ID != "event-3" || events[1].ID != "event-2" {
		t.Fatalf("events = %#v, want [event-3, event-2]", events)
	}
}

func TestListAllMonitorEventsOrdersNewestFirstAcrossRepositoriesAndRespectsLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitorA := testMonitor("C:/workspace/repo-a", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitorA); err != nil {
		t.Fatalf("create monitor a: %v", err)
	}
	monitorB := testMonitor("C:/workspace/repo-b", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitorB); err != nil {
		t.Fatalf("create monitor b: %v", err)
	}

	repositories := []string{monitorA.Repository, monitorB.Repository, monitorA.Repository}
	for i, id := range []string{"event-1", "event-2", "event-3"} {
		event := testMonitorEvent(id, repositories[i], createdAt.Add(time.Duration(i)*time.Minute))
		if inserted, err := store.InsertMonitorEvent(ctx, event); err != nil || !inserted {
			t.Fatalf("insert %s: inserted=%v err=%v", id, inserted, err)
		}
	}

	events, err := store.ListAllMonitorEvents(ctx, 2, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(events) != 2 || events[0].ID != "event-3" || events[1].ID != "event-2" {
		t.Fatalf("events = %#v, want [event-3, event-2]", events)
	}
	if events[0].Repository != monitorA.Repository || events[1].Repository != monitorB.Repository {
		t.Fatalf("events repositories = %#v, want [%s, %s]", events, monitorA.Repository, monitorB.Repository)
	}
}

func TestListAllMonitorEventsStatusFilter(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	open := testMonitorEvent("event-open", monitor.Repository, createdAt)
	open.Status = protocol.GitHubMonitorEventCompleted
	if inserted, err := store.InsertMonitorEvent(ctx, open); err != nil || !inserted {
		t.Fatalf("insert open: inserted=%v err=%v", inserted, err)
	}
	closed := testMonitorEvent("event-closed", monitor.Repository, createdAt.Add(time.Minute))
	closed.Status = protocol.GitHubMonitorEventClosed
	if inserted, err := store.InsertMonitorEvent(ctx, closed); err != nil || !inserted {
		t.Fatalf("insert closed: inserted=%v err=%v", inserted, err)
	}

	defaultFiltered, err := store.ListAllMonitorEvents(ctx, 10, "")
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if len(defaultFiltered) != 1 || defaultFiltered[0].ID != "event-open" {
		t.Fatalf("default filter events = %#v, want only event-open", defaultFiltered)
	}

	openFiltered, err := store.ListAllMonitorEvents(ctx, 10, "open")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(openFiltered) != 1 || openFiltered[0].ID != "event-open" {
		t.Fatalf("open filter events = %#v, want only event-open", openFiltered)
	}

	completedFiltered, err := store.ListAllMonitorEvents(ctx, 10, "completed")
	if err != nil {
		t.Fatalf("list completed: %v", err)
	}
	if len(completedFiltered) != 1 || completedFiltered[0].ID != "event-open" {
		t.Fatalf("completed filter events = %#v, want only event-open", completedFiltered)
	}

	closedFiltered, err := store.ListAllMonitorEvents(ctx, 10, "closed")
	if err != nil {
		t.Fatalf("list closed: %v", err)
	}
	if len(closedFiltered) != 1 || closedFiltered[0].ID != "event-closed" {
		t.Fatalf("closed filter events = %#v, want only event-closed", closedFiltered)
	}

	allFiltered, err := store.ListAllMonitorEvents(ctx, 10, "all")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(allFiltered) != 2 {
		t.Fatalf("all filter events = %#v, want both events", allFiltered)
	}
}

func TestCloseMonitorEvent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	event := testMonitorEvent("event-1", monitor.Repository, createdAt)
	event.Status = protocol.GitHubMonitorEventCompleted
	if inserted, err := store.InsertMonitorEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("insert: inserted=%v err=%v", inserted, err)
	}

	closedAt := createdAt.Add(time.Minute)
	if err := store.CloseMonitorEvent(ctx, event.ID, closedAt); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := store.GetMonitorEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != protocol.GitHubMonitorEventClosed || got.ClosedAt == nil || !got.ClosedAt.Equal(closedAt) {
		t.Fatalf("got = %#v", got)
	}

	// Closing again is idempotent and must not move ClosedAt.
	if err := store.CloseMonitorEvent(ctx, event.ID, closedAt.Add(time.Hour)); err != nil {
		t.Fatalf("re-close: %v", err)
	}
	got, err = store.GetMonitorEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.ClosedAt.Equal(closedAt) {
		t.Fatalf("closedAt moved on re-close: %#v, want %v", got.ClosedAt, closedAt)
	}

	if err := store.CloseMonitorEvent(ctx, "missing", closedAt); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCloseMonitorEventForSession(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	event := testMonitorEvent("event-1", monitor.Repository, createdAt)
	event.Status = protocol.GitHubMonitorEventRunStarted
	if inserted, err := store.InsertMonitorEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("insert: inserted=%v err=%v", inserted, err)
	}
	session := protocol.AgentSession{
		ID: "session-1", Agent: protocol.AgentCodex, Workspace: monitor.Repository,
		Status: protocol.SessionActive, CreatedAt: createdAt,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.AttachMonitorEventSession(ctx, event.ID, session.ID, createdAt); err != nil {
		t.Fatalf("attach session: %v", err)
	}

	// No event references "session-without-a-job": a silent no-op.
	if err := store.CloseMonitorEventForSession(ctx, "session-without-a-job", createdAt); err != nil {
		t.Fatalf("close for unrelated session: %v", err)
	}

	closedAt := createdAt.Add(time.Minute)
	if err := store.CloseMonitorEventForSession(ctx, session.ID, closedAt); err != nil {
		t.Fatalf("close for session: %v", err)
	}
	got, err := store.GetMonitorEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != protocol.GitHubMonitorEventClosed || got.ClosedAt == nil || !got.ClosedAt.Equal(closedAt) {
		t.Fatalf("got = %#v", got)
	}
}

func TestListMonitorEventsByStatusForOutboxDispatcher(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := newTestRepositoryForEvents(t, store, createdAt)

	queued := testMonitorEvent("event-queued", monitor.Repository, createdAt)
	queued.Status = protocol.GitHubMonitorEventQueued
	if inserted, err := store.InsertMonitorEvent(ctx, queued); err != nil || !inserted {
		t.Fatalf("insert queued: inserted=%v err=%v", inserted, err)
	}
	detected := testMonitorEvent("event-detected", monitor.Repository, createdAt)
	if inserted, err := store.InsertMonitorEvent(ctx, detected); err != nil || !inserted {
		t.Fatalf("insert detected: inserted=%v err=%v", inserted, err)
	}

	queuedEvents, err := store.ListMonitorEventsByStatus(ctx, protocol.GitHubMonitorEventQueued, 10)
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(queuedEvents) != 1 || queuedEvents[0].ID != "event-queued" {
		t.Fatalf("queuedEvents = %#v", queuedEvents)
	}
}

func TestMonitorEventUpdateMissingReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.UpdateMonitorEventStatus(ctx, "missing", protocol.GitHubMonitorEventMatched, time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := store.AttachMonitorEventSession(ctx, "missing", "session-1", time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := store.AttachMonitorEventRun(ctx, "missing", "run-1", time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := store.SkipMonitorEvent(ctx, "missing", "reason", time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := store.FailMonitorEvent(ctx, "missing", "error", time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func strPtr(value string) *string { return &value }
