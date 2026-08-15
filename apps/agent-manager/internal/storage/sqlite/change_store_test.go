package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func TestReplaceAndGetChangeSet(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	session := testSession("session-changes")
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	oldPath, newPath := "old.txt", "new.txt"
	original, modified := "before\n", "after\n"
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{
		{
			ID: "file-1", OldPath: &oldPath, NewPath: &newPath, Kind: protocol.FileModify,
			Original: &original, Modified: &modified, ReviewMode: "hunk", Status: protocol.ReviewPending,
			Hunks: []protocol.ChangeHunk{
				{ID: "hunk-1", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, OriginalText: original, ModifiedText: modified, Status: protocol.ReviewPending},
			},
		},
		{ID: "file-2", NewPath: stringPtr("image.png"), Kind: protocol.FileBinary, ReviewMode: "file", Status: protocol.ReviewPending, Hunks: []protocol.ChangeHunk{}},
	}}
	if err := store.ReplaceChangeSet(ctx, changeSet); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := store.GetChangeSet(ctx, session.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(got, changeSet) {
		t.Fatalf("got = %#v, want %#v", got, changeSet)
	}

	replacement := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{}}
	if err := store.ReplaceChangeSet(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetChangeSet(ctx, session.ID)
	if err != nil || len(got.Files) != 0 {
		t.Fatalf("empty replacement = %#v, %v", got, err)
	}
}

func TestChangeSetRequiresExistingSession(t *testing.T) {
	store := openTestStore(t)
	_, err := store.GetChangeSet(context.Background(), "missing")
	if err != storage.ErrNotFound {
		t.Fatalf("get error = %v", err)
	}
	if err := store.ReplaceChangeSet(context.Background(), protocol.ChangeSet{SessionID: "missing"}); err != storage.ErrNotFound {
		t.Fatalf("replace error = %v", err)
	}
}

func TestReplaceChangeSetPreservesStableHunkReview(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	session := testSession("session-preserve-review")
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	path := "file.txt"
	changeSet := protocol.ChangeSet{SessionID: session.ID, Files: []protocol.FileChange{{
		ID: "file-version-1", OldPath: &path, NewPath: &path, Kind: protocol.FileModify,
		ReviewMode: "hunk", Status: protocol.ReviewPending,
		Hunks: []protocol.ChangeHunk{{ID: "stable-hunk", Status: protocol.ReviewPending}},
	}}}
	if err := store.ReplaceChangeSet(ctx, changeSet); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHunkReview(ctx, session.ID, "stable-hunk", protocol.ReviewAccepted, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	changeSet.Files[0].ID = "file-version-2"
	changeSet.Files[0].Status = protocol.ReviewPending
	changeSet.Files[0].Hunks[0].Status = protocol.ReviewPending
	if err := store.ReplaceChangeSet(ctx, changeSet); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetChangeSet(ctx, session.ID)
	if err != nil || got.Files[0].Status != protocol.ReviewAccepted || got.Files[0].Hunks[0].Status != protocol.ReviewAccepted {
		t.Fatalf("preserved change set = %#v, err = %v", got, err)
	}
}

func stringPtr(value string) *string { return &value }

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testSession(id string) protocol.AgentSession {
	return protocol.AgentSession{
		ID: id, Agent: protocol.AgentCodex, Workspace: "C:/repository", Worktree: "C:/worktree",
		BaseCommit: "abcdef", Status: protocol.SessionActive, CreatedAt: time.Now().UTC(),
	}
}
