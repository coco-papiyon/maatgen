package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndRemoveDetachedWorktree(t *testing.T) {
	repository := createTestRepository(t)
	manager, err := New()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "worktrees", "session-1")

	worktree, err := manager.Create(context.Background(), repository, destination)
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if worktree.Repository != repository || worktree.Path != destination || worktree.BaseCommit == "" {
		t.Fatalf("worktree = %#v", worktree)
	}
	if got := runGit(t, destination, "rev-parse", "HEAD"); got != worktree.BaseCommit {
		t.Fatalf("worktree HEAD = %q, want %q", got, worktree.BaseCommit)
	}
	if got := runGit(t, destination, "symbolic-ref", "-q", "HEAD"); got != "" {
		t.Fatalf("worktree is attached to %q", got)
	}

	if err := manager.Remove(context.Background(), repository, destination); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still exists: %v", err)
	}
	if err := manager.Remove(context.Background(), repository, destination); err != nil {
		t.Fatalf("remove missing worktree: %v", err)
	}
}

func TestCreateRejectsDirtyAndNonRepositoryWorkspace(t *testing.T) {
	manager, err := New()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	repository := createTestRepository(t)
	if err := os.WriteFile(filepath.Join(repository, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	_, err = manager.Create(context.Background(), repository, filepath.Join(t.TempDir(), "dirty"))
	if !errors.Is(err, ErrDirty) {
		t.Fatalf("dirty error = %v", err)
	}

	_, err = manager.Create(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "not-repository"))
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("non-repository error = %v", err)
	}
}

func createTestRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "maatgen@example.invalid")
	runGit(t, repository, "config", "user.name", "Maatgen Test")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write repository file: %v", err)
	}
	runGit(t, repository, "add", "README.md")
	runGit(t, repository, "commit", "-m", "initial")
	return filepath.Clean(repository)
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		if len(output) == 0 && len(args) > 0 && args[0] == "symbolic-ref" {
			return ""
		}
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
