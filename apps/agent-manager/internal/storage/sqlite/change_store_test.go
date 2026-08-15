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

func TestCheckpointChangeSetLifecycle(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	session := testSession("session-changes")
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	run := protocol.AgentRun{ID: "run-1", SessionID: session.ID, Status: protocol.RunCompleted, Prompt: "change it"}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	cp := protocol.Checkpoint{ID: "checkpoint-1", SessionID: session.ID, RunID: run.ID, HeadCommit: "head", IndexTree: "index", BeforeTree: "before", BeforeRef: "refs/before", CreatedAt: time.Now().UTC()}
	if err := store.CreateCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCheckpoint(ctx, cp.ID, "after", "refs/after", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	oldPath, newPath := "old.txt", "new.txt"
	original, modified := "before\n", "after\n"
	changeSet := protocol.ChangeSet{
		SessionID: session.ID, RunID: run.ID, CheckpointID: cp.ID, BeforeTree: "before", AfterTree: "after",
		Files: []protocol.FileChange{{
			ID: "file-1", OldPath: &oldPath, NewPath: &newPath, Kind: protocol.FileModify,
			Original: &original, Modified: &modified, RestoreMode: "hunk", Status: protocol.RestoreChanged,
			Hunks: []protocol.ChangeHunk{{ID: "hunk-1", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, OriginalText: original, ModifiedText: modified, Status: protocol.RestoreChanged}},
		}},
	}
	if err := store.ReplaceChangeSet(ctx, changeSet); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := store.GetChangeSet(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(got, changeSet) {
		t.Fatalf("got = %#v, want %#v, err = %v", got, changeSet, err)
	}

	if err := store.UpdateHunkRestore(ctx, cp.ID, "hunk-1", protocol.RestoreRestored, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetChangeSetForCheckpoint(ctx, session.ID, cp.ID)
	if err != nil || got.Files[0].Status != protocol.RestoreRestored || got.Files[0].Hunks[0].Status != protocol.RestoreRestored {
		t.Fatalf("restored change set = %#v, err = %v", got, err)
	}
}

func TestChangeSetRequiresExistingSession(t *testing.T) {
	store := openTestStore(t)
	_, err := store.GetChangeSet(context.Background(), "missing")
	if err != storage.ErrNotFound {
		t.Fatalf("get error = %v", err)
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
	return protocol.AgentSession{ID: id, Agent: protocol.AgentCodex, Workspace: "C:/repository", Status: protocol.SessionActive, CreatedAt: time.Now().UTC()}
}
