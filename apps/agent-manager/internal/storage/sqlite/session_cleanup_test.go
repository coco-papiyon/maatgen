package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func TestSessionCleanupLifecycleAndActiveRunGuard(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	session := testSession("session-cleanup")
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	run := protocol.AgentRun{ID: "run-active", SessionID: session.ID, Status: protocol.RunQueued, Prompt: "work"}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareSessionCleanup(ctx, session.ID, time.Now().UTC()); !errors.Is(err, storage.ErrRunActive) {
		t.Fatalf("active run cleanup error = %v", err)
	}

	run.Status = protocol.RunCompleted
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareSessionCleanup(ctx, session.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Status != protocol.SessionClosed || prepared.CleanupStatus != protocol.CleanupPending || prepared.CleanupAttempts != 1 {
		t.Fatalf("prepared = %#v", prepared)
	}
	message := "locked"
	if err := store.FinishSessionCleanup(ctx, session.ID, protocol.CleanupFailed, &message, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	prepared, err = store.PrepareSessionCleanup(ctx, session.ID, time.Now().UTC())
	if err != nil || prepared.CleanupAttempts != 2 || prepared.CleanupError != nil {
		t.Fatalf("retry prepared = %#v, err = %v", prepared, err)
	}
	if err := store.FinishSessionCleanup(ctx, session.ID, protocol.CleanupCompleted, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	completed, err := store.PrepareSessionCleanup(ctx, session.ID, time.Now().UTC())
	if err != nil || completed.CleanupStatus != protocol.CleanupCompleted || completed.CleanupAttempts != 2 {
		t.Fatalf("completed = %#v, err = %v", completed, err)
	}

	newRun := protocol.AgentRun{ID: "run-after-close", SessionID: session.ID, Status: protocol.RunQueued, Prompt: "work"}
	if err := store.CreateRun(ctx, newRun); !errors.Is(err, storage.ErrSessionClosed) {
		t.Fatalf("run after close error = %v", err)
	}
}
