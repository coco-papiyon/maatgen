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

func TestCreateCopilotSession(t *testing.T) {
	store := &fakeStore{}
	service := New(store, &fakeRepositoryManager{root: "C:/repo"})
	service.newID = func() (string, error) { return "session-copilot", nil }
	created, err := service.CreateSession(context.Background(), protocol.CreateSessionRequest{Agent: protocol.AgentCopilot, Workspace: "C:/repo"})
	if err != nil || created.Agent != protocol.AgentCopilot { t.Fatalf("session = %#v, err = %v", created, err) }
}

func TestCreateClaudeSession(t *testing.T) {
	store := &fakeStore{}
	service := New(store, &fakeRepositoryManager{root: "C:/repo"})
	service.newID = func() (string, error) { return "session-claude", nil }
	created, err := service.CreateSession(context.Background(), protocol.CreateSessionRequest{Agent: protocol.AgentClaude, Workspace: "C:/repo"})
	if err != nil || created.Agent != protocol.AgentClaude { t.Fatalf("session = %#v, err = %v", created, err) }
}

func TestCreateSessionRecordsSourceStatsOnce(t *testing.T) {
	store := &fakeStore{}
	analyzer := &fakeAnalyzer{stats: protocol.SourceStats{
		Languages: []protocol.SourceStatsLanguage{{Language: "Go", Files: 2, Code: 100}},
		Total:     protocol.SourceStatsLanguage{Files: 2, Code: 100},
	}}
	service := New(store, &fakeRepositoryManager{root: "C:/repo"}, WithSourceStatsAnalyzer(analyzer))
	service.newID = func() (string, error) { return "session-1", nil }
	created, err := service.CreateSession(context.Background(), protocol.CreateSessionRequest{Agent: protocol.AgentCodex, Workspace: "C:/repo"})
	if err != nil {
		t.Fatal(err)
	}
	service.waitForSourceStats()
	if store.sourceStats.SessionID != created.ID || store.sourceStats.Total.Code != 100 {
		t.Fatalf("source stats = %#v", store.sourceStats)
	}
}

func TestCreateSessionIgnoresSourceStatsFailure(t *testing.T) {
	store := &fakeStore{}
	analyzer := &fakeAnalyzer{err: errors.New("cloc not found")}
	service := New(store, &fakeRepositoryManager{root: "C:/repo"}, WithSourceStatsAnalyzer(analyzer))
	service.newID = func() (string, error) { return "session-1", nil }
	if _, err := service.CreateSession(context.Background(), protocol.CreateSessionRequest{Agent: protocol.AgentCodex, Workspace: "C:/repo"}); err != nil {
		t.Fatalf("session creation must survive analyzer failure: %v", err)
	}
	service.waitForSourceStats()
	if store.sourceStats.SessionID != "" {
		t.Fatalf("source stats must not be saved on analyzer failure: %#v", store.sourceStats)
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
	session     protocol.AgentSession
	closeErr    error
	sourceStats protocol.SourceStats
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
func (f *fakeStore) ReopenSession(_ context.Context, id string) error {
	if f.session.ID != id {
		return storage.ErrNotFound
	}
	f.session.Status = protocol.SessionActive
	f.session.ClosedAt = nil
	return nil
}
func (f *fakeStore) ReplaceSourceStats(_ context.Context, stats protocol.SourceStats) error {
	f.sourceStats = stats
	return nil
}

type fakeAnalyzer struct {
	stats protocol.SourceStats
	err   error
}

func (f *fakeAnalyzer) Analyze(context.Context, string) (protocol.SourceStats, error) {
	return f.stats, f.err
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
