package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func TestCreateSessionUsesRepositoryDirectly(t *testing.T) {
	store := &fakeStore{}
	repositories := &fakeRepositoryManager{root: "C:/repo"}
	service := New(store, repositories)
	service.newID = func() (string, error) { return "session-1", nil }
	service.now = func() time.Time { return time.Unix(1, 0) }
	created, err := service.CreateSession(context.Background(), protocol.CreateSessionRequest{Agent: protocol.AgentCodex, Workspace: "C:/repo/subdir"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Workspace != "C:/repo" || store.session.Workspace != "C:/repo" {
		t.Fatalf("session = %#v", created)
	}
}

func TestCloseSessionRejectsActiveRunAndCleansCheckpointRefs(t *testing.T) {
	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Agent: protocol.AgentCodex, Workspace: "C:/repo", Status: protocol.SessionActive, CreatedAt: time.Now()}, closeErr: storage.ErrRunActive}
	repositories := &fakeRepositoryManager{root: "C:/repo"}
	service := New(store, repositories)
	if _, err := service.CloseSession(context.Background(), "session-1"); !errors.Is(err, ErrRunActive) {
		t.Fatalf("error = %v", err)
	}
	store.closeErr = nil
	closed, err := service.CloseSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != protocol.SessionClosed || repositories.cleaned != "session-1" {
		t.Fatalf("closed=%#v cleanup=%q", closed, repositories.cleaned)
	}
}

type fakeStore struct {
	session  protocol.AgentSession
	closeErr error
}

func (f *fakeStore) CreateSession(_ context.Context, s protocol.AgentSession) error {
	f.session = s
	return nil
}
func (f *fakeStore) GetSession(_ context.Context, id string) (protocol.AgentSession, error) {
	if f.session.ID != id {
		return protocol.AgentSession{}, storage.ErrNotFound
	}
	return f.session, nil
}
func (f *fakeStore) CloseSession(_ context.Context, id string, at time.Time) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	f.session.Status = protocol.SessionClosed
	f.session.ClosedAt = &at
	return nil
}

type fakeRepositoryManager struct {
	root, cleaned string
	err           error
}

func (f *fakeRepositoryManager) ValidateRepository(context.Context, string) (string, error) {
	return f.root, f.err
}
func (f *fakeRepositoryManager) CleanupSession(_ context.Context, _ string, id string) error {
	f.cleaned = id
	return f.err
}
