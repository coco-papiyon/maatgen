package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/pricing"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func TestEventUsageAndRawEventPersistence(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	createdAt := time.Date(2026, 8, 15, 2, 0, 0, 0, time.UTC)
	createSessionAndRun(t, ctx, store, createdAt)

	runID := "run-1"
	event, err := store.AppendEvent(ctx, protocol.SessionEvent{
		ID:        "event-1",
		SessionID: "session-1",
		RunID:     &runID,
		Source:    protocol.EventSourceManager,
		Type:      "run_started",
		Data:      json.RawMessage(`{"status":"running"}`),
		Timestamp: createdAt,
	})
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	if event.Sequence != 1 || event.SchemaVersion != protocol.SchemaVersion {
		t.Fatalf("first event = %#v", event)
	}

	second, err := store.AppendEvent(ctx, protocol.SessionEvent{
		ID:        "event-2",
		SessionID: "session-1",
		RunID:     &runID,
		Source:    protocol.EventSourceCodex,
		Type:      "assistant_message",
		Data:      json.RawMessage(`{"text":"done"}`),
		Timestamp: createdAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("append second event: %v", err)
	}
	if second.Sequence != 2 {
		t.Fatalf("second sequence = %d, want 2", second.Sequence)
	}

	events, err := store.ListEventsAfter(ctx, "session-1", 1, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].ID != "event-2" {
		t.Fatalf("events = %#v", events)
	}

	input, cached, output, reasoning, total := int64(100), int64(40), int64(20), int64(5), int64(120)
	model, aiCredits, costUSD := "gpt-5.4", 0.125, 0.00125
	usage := protocol.TokenUsage{
		InputTokens:           &input,
		CachedInputTokens:     &cached,
		OutputTokens:          &output,
		ReasoningOutputTokens: &reasoning,
		TotalTokens:           &total,
		Model:                 &model,
		AICredits:             &aiCredits,
		CostUSD:               &costUSD,
		Source:                "cli",
	}
	rawUsage := json.RawMessage(`{"input_tokens":100,"output_tokens":20}`)
	if err := store.UpsertRunUsage(ctx, runID, usage, rawUsage); err != nil {
		t.Fatalf("upsert usage: %v", err)
	}
	gotUsage, gotRaw, err := store.GetRunUsage(ctx, runID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if gotUsage.TotalTokens == nil || *gotUsage.TotalTokens != total || gotUsage.Model == nil || *gotUsage.Model != model || gotUsage.AICredits == nil || *gotUsage.AICredits != aiCredits || gotUsage.CostUSD == nil || *gotUsage.CostUSD != costUSD || string(gotRaw) != string(rawUsage) {
		t.Fatalf("usage = %#v, raw = %s", gotUsage, gotRaw)
	}

	rawEvent, err := store.AppendRedactedRawEvent(ctx, storage.RedactedRawEvent{
		SessionID: "session-1",
		RunID:     &runID,
		Agent:     protocol.AgentCodex,
		RawJSON:   json.RawMessage(`{"type":"turn.started","token":"****"}`),
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("append raw event: %v", err)
	}
	if rawEvent.ID == 0 {
		t.Fatal("raw event id was not allocated")
	}
	rawEvents, err := store.ListRedactedRawEvents(ctx, runID, 100)
	if err != nil {
		t.Fatalf("list raw events: %v", err)
	}
	if len(rawEvents) != 1 || rawEvents[0].ID != rawEvent.ID {
		t.Fatalf("raw events = %#v", rawEvents)
	}
}

func TestBackfillRunCosts(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil { t.Fatal(err) }
	defer store.Close()
	createSessionAndRun(t, ctx, store, time.Now().UTC())
	model := "gpt-5.4"
	input, cached, output := int64(1000), int64(200), int64(300)
	if err := store.UpsertModelPricing(ctx, pricing.ModelPricing{Provider: "codex", Model: model, InputPerMillion: 2, CachedInputPerMillion: 0.2, OutputPerMillion: 10, SourceURL: "test", RetrievedAt: time.Now().UTC()}); err != nil { t.Fatal(err) }
	if err := store.UpsertRunUsage(ctx, "run-1", protocol.TokenUsage{InputTokens: &input, CachedInputTokens: &cached, OutputTokens: &output, Source: "cli"}, nil); err != nil { t.Fatal(err) }
	count, err := store.BackfillRunCosts(ctx, map[string]string{"codex": model})
	if err != nil || count != 1 { t.Fatalf("backfill count = %d, err = %v", count, err) }
	usage, _, err := store.GetRunUsage(ctx, "run-1")
	if err != nil || usage.CostUSD == nil || *usage.CostUSD <= 0 { t.Fatalf("usage = %#v, err = %v", usage, err) }
}

func TestRejectsInvalidEventJSON(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	createSessionAndRun(t, ctx, store, time.Now().UTC())

	_, err = store.AppendEvent(ctx, protocol.SessionEvent{
		ID:        "event-1",
		SessionID: "session-1",
		Source:    protocol.EventSourceManager,
		Type:      "error",
		Data:      json.RawMessage(`{`),
	})
	if err == nil {
		t.Fatal("invalid event JSON was accepted")
	}
}

func TestAppendEventPublishesAfterCommit(t *testing.T) {
	ctx := context.Background()
	publisher := &recordingEventPublisher{}
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"), WithEventPublisher(publisher))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	createSessionAndRun(t, ctx, store, time.Now().UTC())

	_, err = store.AppendEvent(ctx, protocol.SessionEvent{
		ID:        "event-1",
		SessionID: "session-1",
		Source:    protocol.EventSourceManager,
		Type:      "run_started",
		Data:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	if len(publisher.sessionIDs) != 1 || publisher.sessionIDs[0] != "session-1" {
		t.Fatalf("published sessions = %#v", publisher.sessionIDs)
	}
}

type recordingEventPublisher struct {
	sessionIDs []string
}

func (p *recordingEventPublisher) Publish(sessionID string) {
	p.sessionIDs = append(p.sessionIDs, sessionID)
}

func createSessionAndRun(t *testing.T, ctx context.Context, store *Store, createdAt time.Time) {
	t.Helper()
	if err := store.CreateSession(ctx, protocol.AgentSession{
		ID:        "session-1",
		Agent:     protocol.AgentCodex,
		Workspace: "C:/workspace/project",
		Status:    protocol.SessionActive,
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.CreateRun(ctx, protocol.AgentRun{
		ID:        "run-1",
		SessionID: "session-1",
		Status:    protocol.RunRunning,
		Prompt:    "Implement the change",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
}
