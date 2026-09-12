package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type sessionReader struct{ session protocol.AgentSession }

func (r sessionReader) GetSession(context.Context, string) (protocol.AgentSession, error) {
	return r.session, nil
}

func TestStatusAndCommitAll(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.name", "Maatgen Test")
	runGit(t, repository, "config", "user.email", "maatgen@example.invalid")
	writeFile(t, repository, "tracked.txt", "before\n")
	runGit(t, repository, "add", "tracked.txt")
	runGit(t, repository, "commit", "-m", "initial")
	writeFile(t, repository, "tracked.txt", "after\n")
	writeFile(t, repository, "new.txt", "new\n")

	service := New(sessionReader{protocol.AgentSession{ID: "session-1", Workspace: repository, WorkspaceKind: protocol.WorkspaceGitRepository}}, "git")
	status, err := service.GetStatus(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Files) != 2 {
		t.Fatalf("files = %#v, want 2 entries", status.Files)
	}
	committed, err := service.Commit(context.Background(), "session-1", "commit from UI")
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Files) != 0 {
		t.Fatalf("files after commit = %#v, want clean", committed.Files)
	}
	if got := string(runGit(t, repository, "log", "-1", "--pretty=%s")); got != "commit from UI\n" {
		t.Fatalf("commit subject = %q", got)
	}
}

func TestStatusShowsUpstreamDivergenceAndPush(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repository := filepath.Join(root, "repository")
	runGit(t, root, "init", "--bare", remote)
	runGit(t, root, "clone", remote, repository)
	runGit(t, repository, "config", "user.name", "Maatgen Test")
	runGit(t, repository, "config", "user.email", "maatgen@example.invalid")
	writeFile(t, repository, "file.txt", "one\n")
	runGit(t, repository, "add", "file.txt")
	runGit(t, repository, "commit", "-m", "initial")
	runGit(t, repository, "push", "-u", "origin", "HEAD")
	writeFile(t, repository, "file.txt", "two\n")
	runGit(t, repository, "commit", "-am", "ahead")

	service := New(sessionReader{protocol.AgentSession{ID: "session-1", Workspace: repository, WorkspaceKind: protocol.WorkspaceGitRepository}}, "git")
	status, err := service.GetStatus(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Ahead != 1 || status.Behind != 0 || status.Upstream == "" || status.RemoteName != "origin" {
		t.Fatalf("status = %#v", status)
	}
	status, err = service.Push(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Ahead != 0 {
		t.Fatalf("ahead after push = %d", status.Ahead)
	}
}

func TestSanitizeRemoteURLRemovesCredentials(t *testing.T) {
	if got := sanitizeRemoteURL("https://user:secret@example.com/owner/repo.git"); got != "https://example.com/owner/repo.git" {
		t.Fatalf("sanitized URL = %q", got)
	}
	if got := sanitizeRemoteURL("git@example.com:owner/repo.git"); got != "git@example.com:owner/repo.git" {
		t.Fatalf("scp-style URL changed to %q", got)
	}
}

func runGit(t *testing.T, directory string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return output
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
