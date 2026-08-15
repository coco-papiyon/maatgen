package changeset

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestGenerateTextChangesAndStableHunkIDs(t *testing.T) {
	repository, _ := createRepository(t)
	manager, _ := checkpoint.New()
	before, err := manager.Capture(context.Background(), repository, "session-1", "run-1", "before")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repository, "modified.txt", "first\nchanged\nthird\n")
	writeFile(t, repository, "added.txt", "new file\n")
	if err := os.Remove(filepath.Join(repository, "deleted.txt")); err != nil {
		t.Fatal(err)
	}

	after, err := manager.Capture(context.Background(), repository, "session-1", "run-1", "after")
	if err != nil {
		t.Fatal(err)
	}
	generator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cp := protocol.Checkpoint{ID: "checkpoint-1", SessionID: "session-1", RunID: "run-1", BeforeTree: before.Tree, AfterTree: &after.Tree}
	first, err := generator.Generate(context.Background(), repository, cp)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	second, err := generator.Generate(context.Background(), repository, cp)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if len(first.Files) != 3 || len(second.Files) != 3 {
		t.Fatalf("files = %#v", first.Files)
	}

	byPath := indexChanges(first)
	modified := byPath["modified.txt"]
	if modified.Kind != protocol.FileModify || modified.RestoreMode != "hunk" || len(modified.Hunks) != 1 {
		t.Fatalf("modified = %#v", modified)
	}
	if modified.Hunks[0].OriginalText != "first\nsecond\nthird\n" || modified.Hunks[0].ModifiedText != "first\nchanged\nthird\n" {
		t.Fatalf("modified hunk = %#v", modified.Hunks[0])
	}
	if byPath["added.txt"].Kind != protocol.FileAdd || len(byPath["added.txt"].Hunks) != 1 {
		t.Fatalf("added = %#v", byPath["added.txt"])
	}
	if byPath["deleted.txt"].Kind != protocol.FileDelete || len(byPath["deleted.txt"].Hunks) != 1 {
		t.Fatalf("deleted = %#v", byPath["deleted.txt"])
	}
	if first.Files[0].ID != second.Files[0].ID || first.Files[0].Hunks[0].ID != second.Files[0].Hunks[0].ID {
		t.Fatal("change IDs are not stable")
	}
}

func TestGenerateRenameBinaryAndModeChange(t *testing.T) {
	repository, _ := createRepository(t)
	manager, _ := checkpoint.New()
	before, err := manager.Capture(context.Background(), repository, "session-1", "run-1", "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repository, "rename.txt"), filepath.Join(repository, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	writeBytes(t, repository, "binary.bin", []byte{0, 1, 2, 3})
	if os.PathSeparator != '\\' {
		if err := os.Chmod(filepath.Join(repository, "mode.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	after, err := manager.Capture(context.Background(), repository, "session-1", "run-1", "after")
	if err != nil {
		t.Fatal(err)
	}
	generator, _ := New()
	cp := protocol.Checkpoint{ID: "checkpoint-1", SessionID: "session-1", RunID: "run-1", BeforeTree: before.Tree, AfterTree: &after.Tree}
	result, err := generator.Generate(context.Background(), repository, cp)
	if err != nil {
		t.Fatal(err)
	}
	byPath := indexChanges(result)
	if rename := byPath["renamed.txt"]; rename.Kind != protocol.FileRename || rename.RestoreMode != "file" || rename.OldPath == nil || *rename.OldPath != "rename.txt" {
		t.Fatalf("rename = %#v", rename)
	}
	if binary := byPath["binary.bin"]; binary.Kind != protocol.FileBinary || binary.Original != nil || binary.Modified != nil {
		t.Fatalf("binary = %#v", binary)
	}
	if os.PathSeparator != '\\' {
		if mode := byPath["mode.sh"]; mode.Kind != protocol.FileModeChange || mode.RestoreMode != "file" {
			t.Fatalf("mode = %#v", mode)
		}
	}
}

func TestParseHunksWithoutFinalNewline(t *testing.T) {
	patch := "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n"
	hunks, err := parseHunks(patch, "file.txt", "file.txt")
	if err != nil || len(hunks) != 1 {
		t.Fatalf("parse = %#v, %v", hunks, err)
	}
	if hunks[0].OriginalText != "old" || hunks[0].ModifiedText != "new" {
		t.Fatalf("hunk = %#v", hunks[0])
	}
}

func createRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "maatgen@example.invalid")
	runGit(t, repository, "config", "user.name", "Maatgen Test")
	writeFile(t, repository, "modified.txt", "first\nsecond\nthird\n")
	writeFile(t, repository, "deleted.txt", "delete me\n")
	writeFile(t, repository, "rename.txt", "rename me\n")
	writeFile(t, repository, "mode.sh", "#!/bin/sh\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "initial")
	return repository, runGit(t, repository, "rev-parse", "HEAD")
}

func indexChanges(changeSet protocol.ChangeSet) map[string]protocol.FileChange {
	result := make(map[string]protocol.FileChange)
	for _, change := range changeSet.Files {
		path := ""
		if change.NewPath != nil {
			path = *change.NewPath
		} else if change.OldPath != nil {
			path = *change.OldPath
		}
		result[path] = change
	}
	return result
}

func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	writeBytes(t, root, path, []byte(content))
}

func writeBytes(t *testing.T, root, path string, content []byte) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
