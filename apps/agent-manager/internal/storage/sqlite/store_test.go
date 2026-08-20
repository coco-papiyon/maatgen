package sqlite

import (
	"context"
	"database/sql"
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
		ID:        "session-1",
		Agent:     protocol.AgentCodex,
		Workspace: "C:/workspace/project",
		Status:    protocol.SessionActive,
		CreatedAt: createdAt,
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
	if err != nil || gotSession.AgentThreadID == nil || *gotSession.AgentThreadID != threadID {
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

	sessions, err := store.ListSessions(ctx, 10, nil, "")
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

func TestCloseSessionRejectsActiveRun(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session := protocol.AgentSession{ID: "session-active", Agent: protocol.AgentCodex, Workspace: "C:/workspace", Status: protocol.SessionActive, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, protocol.AgentRun{ID: "run-active", SessionID: session.ID, Status: protocol.RunRunning, Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CloseSession(ctx, session.ID, time.Now().UTC()); !errors.Is(err, storage.ErrRunActive) {
		t.Fatalf("close error = %v", err)
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
			Workspace: "C:/workspace",
			Status:    protocol.SessionActive, CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("create session %d: %v", index, err)
		}
	}

	first, err := store.ListSessions(ctx, 2, nil, "")
	if err != nil || len(first) != 2 || first[0].ID != "session-3" || first[1].ID != "session-2" {
		t.Fatalf("first page = %#v, err = %v", first, err)
	}
	second, err := store.ListSessions(ctx, 2, &protocol.SessionCursor{CreatedAt: first[1].CreatedAt, ID: first[1].ID}, "")
	if err != nil || len(second) != 1 || second[0].ID != "session-1" {
		t.Fatalf("second page = %#v, err = %v", second, err)
	}
}

func TestListSessionsFiltersByStatus(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	base := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	active := protocol.AgentSession{ID: "session-active", Agent: protocol.AgentCodex, Workspace: "C:/workspace", Status: protocol.SessionActive, CreatedAt: base}
	closed := protocol.AgentSession{ID: "session-closed", Agent: protocol.AgentCodex, Workspace: "C:/workspace", Status: protocol.SessionActive, CreatedAt: base.Add(time.Minute)}
	if err := store.CreateSession(ctx, active); err != nil {
		t.Fatalf("create active session: %v", err)
	}
	if err := store.CreateSession(ctx, closed); err != nil {
		t.Fatalf("create closed session: %v", err)
	}
	if err := store.CloseSession(ctx, closed.ID, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("close session: %v", err)
	}

	activeOnly, err := store.ListSessions(ctx, 10, nil, protocol.SessionActive)
	if err != nil || len(activeOnly) != 1 || activeOnly[0].ID != active.ID {
		t.Fatalf("active only = %#v, err = %v", activeOnly, err)
	}

	closedOnly, err := store.ListSessions(ctx, 10, nil, protocol.SessionClosed)
	if err != nil || len(closedOnly) != 1 || closedOnly[0].ID != closed.ID {
		t.Fatalf("closed only = %#v, err = %v", closedOnly, err)
	}

	all, err := store.ListSessions(ctx, 10, nil, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %#v, err = %v", all, err)
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

func TestMultiAgentMigrationPreservesCodexSessions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "manager.db")
	db, err := sql.Open("sqlite", path)
	if err != nil { t.Fatal(err) }
	for version, name := range []string{"001_initial.sql", "002_change_store.sql", "003_session_cleanup.sql", "004_direct_checkpoints.sql"} {
		content, readErr := migrationsFS.ReadFile("migrations/" + name)
		if readErr != nil { t.Fatal(readErr) }
		if _, err = db.ExecContext(ctx, string(content)); err != nil { t.Fatalf("apply %s: %v", name, err) }
		if _, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", version+1, formatTime(time.Now().UTC())); err != nil { t.Fatal(err) }
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO sessions(id, agent, workspace, codex_thread_id, status, created_at) VALUES ('session-old', 'codex', 'C:/repo', 'thread-old', 'active', '2026-08-15T00:00:00Z')`); err != nil { t.Fatal(err) }
	if _, err = db.ExecContext(ctx, `INSERT INTO runs(id, session_id, status, prompt, created_at) VALUES ('run-old', 'session-old', 'completed', 'done', '2026-08-15T00:00:00Z')`); err != nil { t.Fatal(err) }
	if _, err = db.ExecContext(ctx, `INSERT INTO events(id, session_id, run_id, sequence, source, event_type, schema_version, payload_json, created_at) VALUES ('event-old', 'session-old', 'run-old', 1, 'codex', 'run_completed', 1, '{}', '2026-08-15T00:00:00Z')`); err != nil { t.Fatal(err) }
	if err = db.Close(); err != nil { t.Fatal(err) }

	store, err := Open(ctx, path)
	if err != nil { t.Fatalf("upgrade store: %v", err) }
	defer store.Close()
	session, err := store.GetSession(ctx, "session-old")
	if err != nil || session.AgentThreadID == nil || *session.AgentThreadID != "thread-old" { t.Fatalf("session = %#v, err = %v", session, err) }
	if _, err = store.GetRun(ctx, "run-old"); err != nil { t.Fatalf("preserved run: %v", err) }
	events, err := store.ListEventsAfter(ctx, "session-old", 0, 10)
	if err != nil || len(events) != 1 || events[0].Source != protocol.EventSourceCodex { t.Fatalf("events = %#v, err = %v", events, err) }
}
