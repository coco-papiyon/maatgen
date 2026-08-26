package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestCloseSessionCascadesToItsGitHubMonitorEvent(t *testing.T) {
	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Agent: protocol.AgentCodex, Workspace: "C:/repo", Status: protocol.SessionActive, CreatedAt: time.Now()}}
	service := New(store, &fakeRepositoryManager{root: "C:/repo"})
	if _, err := service.CloseSession(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if store.closedMonitorEventForSession != "session-1" {
		t.Fatalf("closedMonitorEventForSession = %q, want session-1", store.closedMonitorEventForSession)
	}
}

func TestGetWorkspaceFileTreeListsTopLevelAndSkipsExcludedDirs(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "node_modules"))
	mustWriteFile(t, filepath.Join(root, "node_modules", "ignored.js"), "ignored")
	mustMkdir(t, filepath.Join(root, "src"))
	mustWriteFile(t, filepath.Join(root, "src", "index.ts"), "export {};\n")
	mustMkdir(t, filepath.Join(root, "empty"))
	mustWriteFile(t, filepath.Join(root, "README.md"), "# hello\n")

	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Workspace: root}}
	service := New(store, &fakeRepositoryManager{root: root})
	nodes, err := service.GetWorkspaceFileTree(context.Background(), "session-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %#v", nodes)
	}
	// Directories sort before files, so "empty" and "src" come before "README.md".
	if nodes[0].Name != "empty" || nodes[0].Type != "dir" || nodes[0].HasChildren {
		t.Fatalf("nodes[0] = %#v", nodes[0])
	}
	if nodes[1].Name != "src" || nodes[1].Type != "dir" || !nodes[1].HasChildren {
		t.Fatalf("nodes[1] = %#v", nodes[1])
	}
	if nodes[2].Name != "README.md" || nodes[2].Type != "file" {
		t.Fatalf("nodes[2] = %#v", nodes[2])
	}
}

func TestGetWorkspaceFileTreeListsSubdirectoryOnDemand(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "src"))
	mustWriteFile(t, filepath.Join(root, "src", "index.ts"), "export {};\n")
	mustMkdir(t, filepath.Join(root, "src", "nested"))
	mustWriteFile(t, filepath.Join(root, "src", "nested", "deep.ts"), "export {};\n")

	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Workspace: root}}
	service := New(store, &fakeRepositoryManager{root: root})
	nodes, err := service.GetWorkspaceFileTree(context.Background(), "session-1", "src")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Path != "src/nested" || !nodes[0].HasChildren || nodes[1].Path != "src/index.ts" {
		t.Fatalf("nodes = %#v", nodes)
	}
}

func TestGetWorkspaceFileTreeRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "sub"))

	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Workspace: root}}
	service := New(store, &fakeRepositoryManager{root: root})
	if _, err := service.GetWorkspaceFileTree(context.Background(), "session-1", "../../../../etc"); err == nil {
		t.Fatal("expected an error for a path escaping the workspace")
	}
}

func TestReadWorkspaceFileReturnsContent(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "docs"))
	mustWriteFile(t, filepath.Join(root, "docs", "notes.md"), "# Notes\n")

	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Workspace: root}}
	service := New(store, &fakeRepositoryManager{root: root})
	content, err := service.ReadWorkspaceFile(context.Background(), "session-1", "docs/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if content.Content != "# Notes\n" || content.Binary || content.Truncated {
		t.Fatalf("content = %#v", content)
	}
}

func TestReadWorkspaceFileRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	secretDir := t.TempDir()
	mustWriteFile(t, filepath.Join(secretDir, "secret.txt"), "top secret")
	mustMkdir(t, filepath.Join(root, "sub"))

	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Workspace: root}}
	service := New(store, &fakeRepositoryManager{root: root})
	// path.Clean anchors ".." against a synthetic root, so escaping segments
	// collapse rather than reaching outside the workspace.
	if _, err := service.ReadWorkspaceFile(context.Background(), "session-1", "../../../../etc/passwd"); err == nil {
		t.Fatal("expected an error for a path escaping the workspace")
	}
}

func TestReadWorkspaceFileDetectsBinary(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "binary.dat"), "\x00\x01\x02binary")

	store := &fakeStore{session: protocol.AgentSession{ID: "session-1", Workspace: root}}
	service := New(store, &fakeRepositoryManager{root: root})
	content, err := service.ReadWorkspaceFile(context.Background(), "session-1", "binary.dat")
	if err != nil {
		t.Fatal(err)
	}
	if !content.Binary || content.Content != "" {
		t.Fatalf("content = %#v", content)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeStore struct {
	session                      protocol.AgentSession
	closeErr                     error
	sourceStats                  protocol.SourceStats
	closedMonitorEventForSession string
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
func (f *fakeStore) CloseMonitorEventForSession(_ context.Context, sessionID string, _ time.Time) error {
	f.closedMonitorEventForSession = sessionID
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
