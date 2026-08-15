package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/gitworktree"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func TestCreateSessionPersistsPreparedWorktree(t *testing.T) {
	store := &fakeStore{}
	worktrees := &fakeWorktreeManager{created: gitworktree.Worktree{
		Repository: "C:/repository",
		Path:       "C:/data/worktrees/session_test",
		BaseCommit: "abcdef",
	}}
	service := New(store, worktrees, "C:/data/worktrees")
	service.newID = func() (string, error) { return "session_test", nil }
	service.now = func() time.Time { return time.Date(2026, 8, 15, 6, 0, 0, 0, time.UTC) }

	created, err := service.CreateSession(context.Background(), protocol.CreateSessionRequest{
		Agent: protocol.AgentCodex, Workspace: "C:/repository/subdirectory",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.ID != "session_test" || created.Workspace != worktrees.created.Repository || created.BaseCommit != "abcdef" {
		t.Fatalf("created session = %#v", created)
	}
	if worktrees.destination != filepath.Join("C:/data/worktrees", "session_test") || store.created.ID != created.ID {
		t.Fatalf("destination = %q, stored = %#v", worktrees.destination, store.created)
	}
}

func TestCreateSessionRollsBackWorktreeWhenPersistenceFails(t *testing.T) {
	store := &fakeStore{err: errors.New("database unavailable")}
	worktrees := &fakeWorktreeManager{created: gitworktree.Worktree{
		Repository: "C:/repository", Path: "C:/data/worktrees/session_test", BaseCommit: "abcdef",
	}}
	service := New(store, worktrees, "C:/data/worktrees")
	service.newID = func() (string, error) { return "session_test", nil }

	_, err := service.CreateSession(context.Background(), protocol.CreateSessionRequest{
		Agent: protocol.AgentCodex, Workspace: "C:/repository",
	})
	if err == nil {
		t.Fatal("persistence failure was ignored")
	}
	if worktrees.removedPath != worktrees.created.Path || worktrees.removedRepository != worktrees.created.Repository {
		t.Fatalf("rollback repository = %q, path = %q", worktrees.removedRepository, worktrees.removedPath)
	}
}

func TestCloseSessionRemovesWorktreeAndIsIdempotent(t *testing.T) {
	store := &fakeStore{created: protocol.AgentSession{
		ID: "session_test", Workspace: "C:/repository", Worktree: "C:/worktree",
		Status: protocol.SessionActive, CleanupStatus: protocol.CleanupNotStarted,
	}}
	worktrees := &fakeWorktreeManager{}
	service := New(store, worktrees, "C:/data/worktrees")

	closed, err := service.CloseSession(context.Background(), "session_test")
	if err != nil {
		t.Fatalf("close session: %v", err)
	}
	if closed.Status != protocol.SessionClosed || closed.CleanupStatus != protocol.CleanupCompleted || closed.CleanupAttempts != 1 {
		t.Fatalf("closed session = %#v", closed)
	}
	if worktrees.removeCalls != 1 || worktrees.removedPath != "C:/worktree" {
		t.Fatalf("remove calls = %d, path = %q", worktrees.removeCalls, worktrees.removedPath)
	}
	if _, err := service.CloseSession(context.Background(), "session_test"); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if worktrees.removeCalls != 1 {
		t.Fatalf("completed cleanup repeated %d times", worktrees.removeCalls)
	}
}

func TestCloseSessionPersistsFailureAndRetries(t *testing.T) {
	store := &fakeStore{created: protocol.AgentSession{
		ID: "session_test", Workspace: "C:/repository", Worktree: "C:/worktree",
		Status: protocol.SessionActive, CleanupStatus: protocol.CleanupNotStarted,
	}}
	worktrees := &fakeWorktreeManager{removeErr: errors.New("worktree is locked")}
	service := New(store, worktrees, "C:/data/worktrees")

	_, err := service.CloseSession(context.Background(), "session_test")
	if !errors.Is(err, ErrCleanupFailed) {
		t.Fatalf("close error = %v", err)
	}
	if store.created.Status != protocol.SessionClosed || store.created.CleanupStatus != protocol.CleanupFailed || store.created.CleanupError == nil {
		t.Fatalf("failed cleanup state = %#v", store.created)
	}
	worktrees.removeErr = nil
	closed, err := service.CloseSession(context.Background(), "session_test")
	if err != nil {
		t.Fatalf("retry close: %v", err)
	}
	if closed.CleanupStatus != protocol.CleanupCompleted || closed.CleanupAttempts != 2 || closed.CleanupError != nil {
		t.Fatalf("retried cleanup state = %#v", closed)
	}
}

func TestCloseSessionRejectsActiveRun(t *testing.T) {
	store := &fakeStore{prepareErr: storage.ErrRunActive}
	service := New(store, &fakeWorktreeManager{}, "C:/data/worktrees")
	_, err := service.CloseSession(context.Background(), "session_test")
	if !errors.Is(err, ErrRunActive) {
		t.Fatalf("close error = %v", err)
	}
}

type fakeStore struct {
	created    protocol.AgentSession
	err        error
	prepareErr error
}

func (f *fakeStore) GetSession(_ context.Context, _ string) (protocol.AgentSession, error) {
	return f.created, nil
}

func (f *fakeStore) PrepareSessionCleanup(_ context.Context, _ string, closedAt time.Time) (protocol.AgentSession, error) {
	if f.prepareErr != nil {
		return protocol.AgentSession{}, f.prepareErr
	}
	if f.created.CleanupStatus == protocol.CleanupCompleted {
		return f.created, nil
	}
	f.created.Status = protocol.SessionClosed
	f.created.ClosedAt = &closedAt
	f.created.CleanupStatus = protocol.CleanupPending
	f.created.CleanupError = nil
	f.created.CleanupAttempts++
	return f.created, nil
}

func (f *fakeStore) FinishSessionCleanup(_ context.Context, _ string, status protocol.CleanupStatus, cleanupError *string, updatedAt time.Time) error {
	f.created.CleanupStatus = status
	f.created.CleanupError = cleanupError
	f.created.CleanupUpdatedAt = &updatedAt
	return nil
}

func (f *fakeStore) CreateSession(_ context.Context, created protocol.AgentSession) error {
	f.created = created
	return f.err
}

type fakeWorktreeManager struct {
	created           gitworktree.Worktree
	createErr         error
	destination       string
	removedRepository string
	removedPath       string
	removeErr         error
	removeCalls       int
}

func (f *fakeWorktreeManager) Create(_ context.Context, _ string, destination string) (gitworktree.Worktree, error) {
	f.destination = destination
	return f.created, f.createErr
}

func (f *fakeWorktreeManager) Remove(_ context.Context, repository, worktreePath string) error {
	f.removeCalls++
	f.removedRepository = repository
	f.removedPath = worktreePath
	return f.removeErr
}
