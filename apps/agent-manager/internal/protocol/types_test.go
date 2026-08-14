package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSessionEventFixture(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}

	fixturePath := filepath.Join(
		filepath.Dir(filename),
		"..", "..", "..", "..",
		"packages", "protocol", "fixtures", "session-event.json",
	)

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var event SessionEvent
	if err := json.Unmarshal(content, &event); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if event.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", event.SchemaVersion, SchemaVersion)
	}
	if event.Source != EventSourceManager {
		t.Fatalf("source = %q, want %q", event.Source, EventSourceManager)
	}
}
