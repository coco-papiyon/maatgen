package review

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

func TestAcceptHunksAppliesOnlySelectedChangesAndIsIdempotent(t *testing.T) {
	store, session := createReviewStore(t)
	original := "start\nold one\nmiddle\nold two\nend\n"
	modified := "start\nnew one\nmiddle\nnew two\nend\n"
	writeReviewFile(t, session.Workspace, "file.txt", original)
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{{
		ID: "file-1", OldPath: stringPointer("file.txt"), NewPath: stringPointer("file.txt"),
		Kind: protocol.FileModify, Original: &original, Modified: &modified,
		ReviewMode: "hunk", Status: protocol.ReviewPending,
		Hunks: []protocol.ChangeHunk{
			{ID: "hunk-1", OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 1, OriginalText: "old one\n", ModifiedText: "new one\n", Status: protocol.ReviewPending},
			{ID: "hunk-2", OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1, OriginalText: "old two\n", ModifiedText: "new two\n", Status: protocol.ReviewPending},
		},
	}}}
	if err := store.ReplaceChangeSet(context.Background(), changeSet); err != nil {
		t.Fatal(err)
	}
	service := New(store)

	updated, err := service.Accept(context.Background(), session.ID, "hunk-2")
	if err != nil {
		t.Fatal(err)
	}
	assertReviewFile(t, session.Workspace, "file.txt", "start\nold one\nmiddle\nnew two\nend\n")
	if updated.Files[0].Status != protocol.ReviewPartiallyAccepted || updated.Files[0].Hunks[1].Status != protocol.ReviewAccepted {
		t.Fatalf("review state = %#v", updated.Files[0])
	}
	if _, err := service.Accept(context.Background(), session.ID, "hunk-2"); err != nil {
		t.Fatalf("idempotent accept: %v", err)
	}
	updated, err = service.Accept(context.Background(), session.ID, "hunk-1")
	if err != nil {
		t.Fatal(err)
	}
	assertReviewFile(t, session.Workspace, "file.txt", modified)
	if updated.Files[0].Status != protocol.ReviewAccepted {
		t.Fatalf("file status = %s", updated.Files[0].Status)
	}
	events, err := store.ListEventsAfter(context.Background(), session.ID, 0, 10)
	if err != nil || len(events) != 2 || events[0].Type != protocol.EventTypeChangeReviewed || events[1].Type != protocol.EventTypeChangeReviewed {
		t.Fatalf("review events = %#v, err = %v", events, err)
	}
}

func TestAcceptDetectsExternalWorkingTreeChange(t *testing.T) {
	store, session := createReviewStore(t)
	original, modified := "before\n", "after\n"
	writeReviewFile(t, session.Workspace, "file.txt", "external\n")
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{{
		ID: "file-1", OldPath: stringPointer("file.txt"), NewPath: stringPointer("file.txt"), Kind: protocol.FileModify,
		Original: &original, Modified: &modified, ReviewMode: "hunk", Status: protocol.ReviewPending,
		Hunks: []protocol.ChangeHunk{{ID: "hunk-1", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, OriginalText: original, ModifiedText: modified, Status: protocol.ReviewPending}},
	}}}
	if err := store.ReplaceChangeSet(context.Background(), changeSet); err != nil {
		t.Fatal(err)
	}
	_, err := New(store).Accept(context.Background(), session.ID, "hunk-1")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("accept error = %v", err)
	}
}

func TestRejectDoesNotModifyWorkingTree(t *testing.T) {
	store, session := createReviewStore(t)
	original, modified := "before\n", "after\n"
	writeReviewFile(t, session.Workspace, "file.txt", original)
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{{
		ID: "file-1", OldPath: stringPointer("file.txt"), NewPath: stringPointer("file.txt"), Kind: protocol.FileModify,
		Original: &original, Modified: &modified, ReviewMode: "hunk", Status: protocol.ReviewPending,
		Hunks: []protocol.ChangeHunk{{ID: "hunk-1", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, OriginalText: original, ModifiedText: modified, Status: protocol.ReviewPending}},
	}}}
	if err := store.ReplaceChangeSet(context.Background(), changeSet); err != nil {
		t.Fatal(err)
	}
	updated, err := New(store).Reject(context.Background(), session.ID, "hunk-1")
	if err != nil || updated.Files[0].Status != protocol.ReviewRejected {
		t.Fatalf("reject = %#v, %v", updated, err)
	}
	assertReviewFile(t, session.Workspace, "file.txt", original)
}

func TestAcceptFileAddsBinaryFromAgentWorktree(t *testing.T) {
	store, session := createReviewStore(t)
	content := []byte{0, 1, 2, 3}
	if err := os.WriteFile(filepath.Join(session.Worktree, "asset.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{{
		ID: "file-binary", NewPath: stringPointer("asset.bin"), Kind: protocol.FileBinary,
		ReviewMode: "file", Status: protocol.ReviewPending, Hunks: []protocol.ChangeHunk{},
	}}}
	if err := store.ReplaceChangeSet(context.Background(), changeSet); err != nil {
		t.Fatal(err)
	}
	updated, err := New(store).Accept(context.Background(), session.ID, "file-binary")
	if err != nil || updated.Files[0].Status != protocol.ReviewAccepted {
		t.Fatalf("accept binary = %#v, %v", updated, err)
	}
	actual, err := os.ReadFile(filepath.Join(session.Workspace, "asset.bin"))
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("binary = %v, err = %v", actual, err)
	}
}

func TestAcceptAllAppliesPendingChangesAndKeepsSessionActive(t *testing.T) {
	store, session := createReviewStore(t)
	original := "one\ntwo\n"
	modified := "ONE\nTWO\n"
	writeReviewFile(t, session.Workspace, "all.txt", original)
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{{
		ID: "file-all", OldPath: stringPointer("all.txt"), NewPath: stringPointer("all.txt"),
		Kind: protocol.FileModify, Original: &original, Modified: &modified,
		ReviewMode: "hunk", Status: protocol.ReviewPending,
		Hunks: []protocol.ChangeHunk{
			{ID: "hunk-all-1", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, OriginalText: "one\n", ModifiedText: "ONE\n", Status: protocol.ReviewPending},
			{ID: "hunk-all-2", OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 1, OriginalText: "two\n", ModifiedText: "TWO\n", Status: protocol.ReviewPending},
		},
	}}}
	if err := store.ReplaceChangeSet(context.Background(), changeSet); err != nil {
		t.Fatal(err)
	}
	closer := &recordingCloser{store: store}
	service := New(store, WithSessionCloser(closer))
	updated, err := service.AcceptAll(context.Background(), session.ID)
	if err != nil || updated.Files[0].Status != protocol.ReviewAccepted {
		t.Fatalf("accept all = %#v, %v", updated, err)
	}
	assertReviewFile(t, session.Workspace, "all.txt", modified)
	closed, err := store.GetSession(context.Background(), session.ID)
	if err != nil || closed.Status != protocol.SessionActive || closer.calls != 0 {
		t.Fatalf("session = %#v, close calls = %d, err = %v", closed, closer.calls, err)
	}
	if _, err := service.AcceptAll(context.Background(), session.ID); err != nil || closer.calls != 0 {
		t.Fatalf("idempotent accept all: calls = %d, err = %v", closer.calls, err)
	}
}

func TestRejectAllLeavesFilesUnchangedAndKeepsSessionActive(t *testing.T) {
	store, session := createReviewStore(t)
	original, modified := "before\n", "after\n"
	writeReviewFile(t, session.Workspace, "reject-all.txt", original)
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{{
		ID: "file-reject-all", OldPath: stringPointer("reject-all.txt"), NewPath: stringPointer("reject-all.txt"),
		Kind: protocol.FileModify, Original: &original, Modified: &modified, ReviewMode: "hunk", Status: protocol.ReviewPending,
		Hunks: []protocol.ChangeHunk{{ID: "hunk-reject-all", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, OriginalText: original, ModifiedText: modified, Status: protocol.ReviewPending}},
	}}}
	if err := store.ReplaceChangeSet(context.Background(), changeSet); err != nil {
		t.Fatal(err)
	}
	closer := &recordingCloser{store: store}
	service := New(store, WithSessionCloser(closer))
	updated, err := service.RejectAll(context.Background(), session.ID)
	if err != nil || updated.Files[0].Status != protocol.ReviewRejected {
		t.Fatalf("reject all = %#v, %v", updated, err)
	}
	assertReviewFile(t, session.Workspace, "reject-all.txt", original)
	current, err := store.GetSession(context.Background(), session.ID)
	if err != nil || current.Status != protocol.SessionActive || closer.calls != 0 {
		t.Fatalf("session = %#v, close calls = %d, err = %v", current, closer.calls, err)
	}
}

func createReviewStore(t *testing.T) (*storesqlite.Store, protocol.AgentSession) {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "maatgen@example.invalid")
	runGit(t, repository, "config", "user.name", "Maatgen Test")
	writeReviewFile(t, repository, "seed.txt", "seed\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "base")
	store, err := storesqlite.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := protocol.AgentSession{
		ID: "session-review", Agent: protocol.AgentCodex, Workspace: repository, Worktree: t.TempDir(),
		BaseCommit: runGit(t, repository, "rev-parse", "HEAD"), Status: protocol.SessionActive,
		CreatedAt: time.Now().UTC(), CleanupStatus: protocol.CleanupNotStarted,
	}
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return store, session
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeReviewFile(t *testing.T, root, path, content string) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertReviewFile(t *testing.T, root, path, expected string) {
	t.Helper()
	actual, err := os.ReadFile(filepath.Join(root, path))
	if err != nil || string(actual) != expected {
		t.Fatalf("file = %q, err = %v, want %q", actual, err, expected)
	}
}

func stringPointer(value string) *string { return &value }

type recordingCloser struct {
	store *storesqlite.Store
	calls int
}

func (f *recordingCloser) CloseSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	f.calls++
	session, err := f.store.PrepareSessionCleanup(ctx, id, time.Now().UTC())
	if err != nil {
		return protocol.AgentSession{}, err
	}
	if err := f.store.FinishSessionCleanup(ctx, id, protocol.CleanupCompleted, nil, time.Now().UTC()); err != nil {
		return protocol.AgentSession{}, err
	}
	return f.store.GetSession(ctx, session.ID)
}

var _ SessionCloser = (*recordingCloser)(nil)
