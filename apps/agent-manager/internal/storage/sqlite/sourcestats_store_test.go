package sqlite

import (
	"context"
	"reflect"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestSourceStatsLifecycle(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	session := testSession("session-source-stats")
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	empty, err := store.GetSourceStats(ctx, session.ID)
	if err != nil || len(empty.Languages) != 0 || empty.Total != (protocol.SourceStatsLanguage{}) {
		t.Fatalf("empty stats = %#v, err = %v", empty, err)
	}

	stats := protocol.SourceStats{
		SessionID: session.ID,
		Languages: []protocol.SourceStatsLanguage{
			{Language: "Go", Files: 61, Blank: 851, Comment: 60, Code: 9894},
			{Language: "TypeScript", Files: 32, Blank: 231, Comment: 92, Code: 2385},
		},
		Total: protocol.SourceStatsLanguage{Files: 93, Blank: 1082, Comment: 152, Code: 12279},
	}
	if err := store.ReplaceSourceStats(ctx, stats); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := store.GetSourceStats(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(got, stats) {
		t.Fatalf("got = %#v, want %#v, err = %v", got, stats, err)
	}

	// Replacing again (as happens if a session were re-analyzed) must not
	// accumulate stale rows alongside the fresh ones.
	updated := stats
	updated.Languages = []protocol.SourceStatsLanguage{{Language: "Go", Files: 1, Blank: 1, Comment: 1, Code: 1}}
	updated.Total = protocol.SourceStatsLanguage{Files: 1, Blank: 1, Comment: 1, Code: 1}
	if err := store.ReplaceSourceStats(ctx, updated); err != nil {
		t.Fatalf("replace again: %v", err)
	}
	got, err = store.GetSourceStats(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(got, updated) {
		t.Fatalf("got = %#v, want %#v, err = %v", got, updated, err)
	}
}
