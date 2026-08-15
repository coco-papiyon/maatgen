package restore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/changeset"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

func TestRestoreHunkAndDetectFileConflict(t *testing.T) {
	ctx := context.Background()
	repository := initRepository(t)
	manager, err := checkpoint.New()
	if err != nil {
		t.Fatal(err)
	}
	store, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	session := protocol.AgentSession{ID: "session-1", Agent: protocol.AgentCodex, Workspace: repository, Status: protocol.SessionActive, CreatedAt: time.Now().UTC()}
	run := protocol.AgentRun{ID: "run-1", SessionID: session.ID, Status: protocol.RunCompleted, Prompt: "edit"}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	before, err := manager.Capture(ctx, repository, session.ID, run.ID, "before")
	if err != nil {
		t.Fatal(err)
	}
	cp := protocol.Checkpoint{ID: "checkpoint-1", SessionID: session.ID, RunID: run.ID, HeadCommit: before.HeadCommit, IndexTree: before.IndexTree, BeforeTree: before.Tree, BeforeRef: before.Ref, CreatedAt: time.Now().UTC()}
	if err := store.CreateCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repository, "app.txt"), "line1\nline2 changed\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14 changed\nline15\n")
	writeTestFile(t, filepath.Join(repository, "other.txt"), "after\n")
	after, err := manager.Capture(ctx, repository, session.ID, run.ID, "after")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCheckpoint(ctx, cp.ID, after.Tree, after.Ref, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	cp.AfterTree, cp.AfterRef = &after.Tree, &after.Ref
	generator, err := changeset.New()
	if err != nil {
		t.Fatal(err)
	}
	set, err := generator.Generate(ctx, repository, cp)
	if err != nil || len(set.Files) != 2 {
		t.Fatalf("change set = %#v, err = %v", set, err)
	}
	appFile, otherFile := fileByPath(set, "app.txt"), fileByPath(set, "other.txt")
	if appFile == nil || otherFile == nil || len(appFile.Hunks) != 2 {
		t.Fatalf("unexpected files = %#v", set.Files)
	}
	if err := store.ReplaceChangeSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	service, err := New(store, manager)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := service.RestoreHunk(ctx, session.ID, cp.ID, appFile.Hunks[0].ID)
	if err != nil || fileByPath(restored, "app.txt").Status != protocol.RestorePartiallyRestored {
		t.Fatalf("restored = %#v, err = %v", restored, err)
	}
	if _, err := service.RestoreFile(ctx, session.ID, cp.ID, appFile.ID); err != nil {
		t.Fatalf("restore partially restored file: %v", err)
	}
	content, _ := os.ReadFile(filepath.Join(repository, "app.txt"))
	if string(content) != "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\n" {
		t.Fatalf("content = %q", content)
	}

	// Diverging after the snapshot must never be overwritten by a file restore.
	writeTestFile(t, filepath.Join(repository, "other.txt"), "user edit\n")
	if _, err := service.RestoreFile(ctx, session.ID, cp.ID, otherFile.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}

func initRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "test@example.com")
	runGit(t, repository, "config", "user.name", "Test")
	writeTestFile(t, filepath.Join(repository, "app.txt"), "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\n")
	writeTestFile(t, filepath.Join(repository, "other.txt"), "before\n")
	runGit(t, repository, "add", "app.txt", "other.txt")
	runGit(t, repository, "commit", "-m", "initial")
	return repository
}

func fileByPath(set protocol.ChangeSet, path string) *protocol.FileChange {
	for index := range set.Files {
		if set.Files[index].NewPath != nil && *set.Files[index].NewPath == path {
			return &set.Files[index]
		}
	}
	return nil
}

func runGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
