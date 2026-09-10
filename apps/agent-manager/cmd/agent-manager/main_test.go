package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	sessionservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

type stubRepositoryManager struct{}

func (stubRepositoryManager) ValidateWorkspace(_ context.Context, workspace string) (string, bool, error) {
	return workspace, true, nil
}

func (stubRepositoryManager) CleanupSession(context.Context, string, string) error { return nil }

func openTestStore(t *testing.T) *storesqlite.Store {
	t.Helper()
	store, err := storesqlite.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestResolveStaticDirPrefersExplicitConfiguredPath(t *testing.T) {
	dir := t.TempDir()

	if got := resolveStaticDir(dir, filepath.Join(t.TempDir(), "agent-manager.exe"), t.TempDir()); got != dir {
		t.Fatalf("resolveStaticDir = %q, want %q", got, dir)
	}
	if got := resolveStaticDir(filepath.Join(dir, "missing"), filepath.Join(t.TempDir(), "agent-manager.exe"), t.TempDir()); got != "" {
		t.Fatalf("resolveStaticDir with missing configured path = %q, want empty", got)
	}
}

func TestResolveStaticDirFindsPackagedLayoutNextToExecutable(t *testing.T) {
	exeDir := t.TempDir()
	staticDir := filepath.Join(exeDir, "web", "dist")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("create static dir: %v", err)
	}

	got := resolveStaticDir("", filepath.Join(exeDir, "agent-manager.exe"), t.TempDir())
	if got != staticDir {
		t.Fatalf("resolveStaticDir = %q, want %q", got, staticDir)
	}
}

func TestResolveStaticDirFindsMonorepoDevLayout(t *testing.T) {
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "apps", "agent-manager")
	staticDir := filepath.Join(root, "apps", "web", "dist")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("create static dir: %v", err)
	}

	got := resolveStaticDir("", filepath.Join(t.TempDir(), "agent-manager"), workingDirectory)
	if got != staticDir {
		t.Fatalf("resolveStaticDir = %q, want %q", got, staticDir)
	}
}

func TestResolveStaticDirReturnsEmptyWhenNothingFound(t *testing.T) {
	got := resolveStaticDir("", filepath.Join(t.TempDir(), "agent-manager"), t.TempDir())
	if got != "" {
		t.Fatalf("resolveStaticDir = %q, want empty", got)
	}
}

func TestCloseExpiredSessionsClosesOnlySessionsPastTheCutoff(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	expired := protocol.AgentSession{ID: "session-expired", Agent: protocol.AgentCodex, Workspace: "C:/repo", Status: protocol.SessionActive, CreatedAt: now.Add(-25 * time.Hour)}
	fresh := protocol.AgentSession{ID: "session-fresh", Agent: protocol.AgentCodex, Workspace: "C:/repo", Status: protocol.SessionActive, CreatedAt: now.Add(-time.Hour)}
	alreadyClosed := protocol.AgentSession{ID: "session-closed", Agent: protocol.AgentCodex, Workspace: "C:/repo", Status: protocol.SessionActive, CreatedAt: now.Add(-48 * time.Hour)}
	for _, session := range []protocol.AgentSession{expired, fresh, alreadyClosed} {
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("create session %s: %v", session.ID, err)
		}
	}
	if err := store.CloseSession(ctx, alreadyClosed.ID, now.Add(-40*time.Hour)); err != nil {
		t.Fatalf("pre-close session: %v", err)
	}

	sessions := sessionservice.New(store, stubRepositoryManager{})
	closed, err := closeExpiredSessions(ctx, store, sessions, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("close expired sessions: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}

	if got, err := store.GetSession(ctx, expired.ID); err != nil || got.Status != protocol.SessionClosed {
		t.Fatalf("expired session = %#v, err = %v", got, err)
	}
	if got, err := store.GetSession(ctx, fresh.ID); err != nil || got.Status != protocol.SessionActive {
		t.Fatalf("fresh session = %#v, err = %v", got, err)
	}
}

func TestCloseExpiredSessionsSkipsSessionsWithActiveRuns(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	busy := protocol.AgentSession{ID: "session-busy", Agent: protocol.AgentCodex, Workspace: "C:/repo", Status: protocol.SessionActive, CreatedAt: now.Add(-25 * time.Hour)}
	if err := store.CreateSession(ctx, busy); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.CreateRun(ctx, protocol.AgentRun{ID: "run-busy", SessionID: busy.ID, Status: protocol.RunRunning, Prompt: "work"}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	sessions := sessionservice.New(store, stubRepositoryManager{})
	closed, err := closeExpiredSessions(ctx, store, sessions, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("close expired sessions: %v", err)
	}
	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if got, err := store.GetSession(ctx, busy.ID); err != nil || got.Status != protocol.SessionActive {
		t.Fatalf("session with active run = %#v, err = %v", got, err)
	}
}

func TestRecordInterruptedRunFailuresAppendsRunFailedEvent(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	session := protocol.AgentSession{ID: "session-interrupted", Agent: protocol.AgentClaude, Workspace: "C:/repo", Status: protocol.SessionActive, CreatedAt: now.Add(-time.Hour)}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.CreateRun(ctx, protocol.AgentRun{ID: "run-interrupted", SessionID: session.ID, Status: protocol.RunRunning, Prompt: "work"}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	interrupted, err := store.FailInterruptedRuns(ctx, now)
	if err != nil {
		t.Fatalf("fail interrupted runs: %v", err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("interrupted = %#v, want 1 entry", interrupted)
	}

	recordInterruptedRunFailures(ctx, store, interrupted, now)

	events, err := store.ListEventsAfter(ctx, session.ID, 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var found *protocol.SessionEvent
	for i := range events {
		if events[i].Type == protocol.EventTypeRunFailed {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("events = %#v, want a run_failed event", events)
	}
	if found.RunID == nil || *found.RunID != "run-interrupted" {
		t.Fatalf("run_failed event run id = %v, want run-interrupted", found.RunID)
	}
	if found.Source != protocol.EventSourceManager {
		t.Fatalf("run_failed event source = %q, want manager", found.Source)
	}

	// The session's composer-blocking status is now gone from the DB too.
	sessions, err := store.ListSessions(ctx, 10, nil, "")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ActiveRunStatus != nil {
		t.Fatalf("sessions = %#v, want a single session with no active run status", sessions)
	}
}

func TestCloseExpiredSessionsSkipsSessionsCreatedFromAJob(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	eventID := "event-1"
	fromJob := protocol.AgentSession{
		ID: "session-from-job", Agent: protocol.AgentCodex, Workspace: "C:/repo", Status: protocol.SessionActive,
		TriggerSource: protocol.TriggerSourceGitHubMonitor, GitHubMonitorEvent: &eventID, CreatedAt: now.Add(-25 * time.Hour),
	}
	if err := store.CreateSession(ctx, fromJob); err != nil {
		t.Fatalf("create session: %v", err)
	}

	sessions := sessionservice.New(store, stubRepositoryManager{})
	closed, err := closeExpiredSessions(ctx, store, sessions, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("close expired sessions: %v", err)
	}
	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if got, err := store.GetSession(ctx, fromJob.ID); err != nil || got.Status != protocol.SessionActive {
		t.Fatalf("session from job = %#v, err = %v", got, err)
	}
}

func TestCloseExpiredSessionsPaginatesPastTheFirstPage(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 120; index++ {
		session := protocol.AgentSession{
			ID: "session-" + strconv.Itoa(index), Agent: protocol.AgentCodex, Workspace: "C:/repo",
			Status: protocol.SessionActive, CreatedAt: now.Add(-48 * time.Hour).Add(time.Duration(index) * time.Second),
		}
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("create session %d: %v", index, err)
		}
	}

	sessions := sessionservice.New(store, stubRepositoryManager{})
	closed, err := closeExpiredSessions(ctx, store, sessions, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("close expired sessions: %v", err)
	}
	if closed != 120 {
		t.Fatalf("closed = %d, want 120", closed)
	}
}
