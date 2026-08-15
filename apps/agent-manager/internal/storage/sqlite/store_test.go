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

func TestSessionAndRunLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	createdAt := time.Date(2026, 8, 15, 1, 2, 3, 4, time.UTC)
	session := protocol.AgentSession{
		ID:         "session-1",
		Agent:      protocol.AgentCodex,
		Workspace:  "C:/workspace/project",
		Worktree:   "C:/data/maatgen/worktrees/session-1",
		BaseCommit: "0123456789abcdef",
		Status:     protocol.SessionActive,
		CreatedAt:  createdAt,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	gotSession, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if gotSession.ID != session.ID || !gotSession.CreatedAt.Equal(createdAt) {
		t.Fatalf("session = %#v, want %#v", gotSession, session)
	}

	threadID := "thread-1"
	if err := store.UpdateSessionThreadID(ctx, session.ID, threadID); err != nil {
		t.Fatalf("update thread id: %v", err)
	}
	gotSession, err = store.GetSession(ctx, session.ID)
	if err != nil || gotSession.CodexThreadID == nil || *gotSession.CodexThreadID != threadID {
		t.Fatalf("updated session = %#v, err = %v", gotSession, err)
	}

	run := protocol.AgentRun{
		ID:        "run-1",
		SessionID: session.ID,
		Status:    protocol.RunQueued,
		Prompt:    "Implement the change",
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	startedAt := createdAt.Add(time.Minute)
	finishedAt := startedAt.Add(2 * time.Minute)
	exitCode := 0
	run.Status = protocol.RunCompleted
	run.StartedAt = &startedAt
	run.FinishedAt = &finishedAt
	run.ExitCode = &exitCode
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	gotRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.Status != protocol.RunCompleted || gotRun.ExitCode == nil || *gotRun.ExitCode != 0 {
		t.Fatalf("run = %#v", gotRun)
	}

	closedAt := finishedAt.Add(time.Minute)
	if err := store.CloseSession(ctx, session.ID, closedAt); err != nil {
		t.Fatalf("close session: %v", err)
	}
	gotSession, err = store.GetSession(ctx, session.ID)
	if err != nil || gotSession.Status != protocol.SessionClosed || gotSession.ClosedAt == nil {
		t.Fatalf("closed session = %#v, err = %v", gotSession, err)
	}

	sessions, err := store.ListSessions(ctx, 10, nil)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, err = %v", sessions, err)
	}
}

func TestNotFoundAndForeignKey(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if _, err := store.GetSession(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("get missing session error = %v", err)
	}

	err = store.CreateRun(ctx, protocol.AgentRun{
		ID:        "run-1",
		SessionID: "missing",
		Status:    protocol.RunQueued,
		Prompt:    "invalid",
	})
	if err == nil {
		t.Fatal("create run without session succeeded")
	}
}

func TestListSessionsWithKeysetCursor(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	base := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	for index := 1; index <= 3; index++ {
		session := protocol.AgentSession{
			ID: "session-" + string(rune('0'+index)), Agent: protocol.AgentCodex,
			Workspace: "C:/workspace", Worktree: "C:/worktree", BaseCommit: "abcdef",
			Status: protocol.SessionActive, CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("create session %d: %v", index, err)
		}
	}

	first, err := store.ListSessions(ctx, 2, nil)
	if err != nil || len(first) != 2 || first[0].ID != "session-3" || first[1].ID != "session-2" {
		t.Fatalf("first page = %#v, err = %v", first, err)
	}
	second, err := store.ListSessions(ctx, 2, &protocol.SessionCursor{CreatedAt: first[1].CreatedAt, ID: first[1].ID})
	if err != nil || len(second) != 1 || second[0].ID != "session-1" {
		t.Fatalf("second page = %#v, err = %v", second, err)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "manager.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
}
