package runtimeinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "manager.json")
	want := Metadata{
		PID:           123,
		Address:       "127.0.0.1:3100",
		Version:       "test",
		SchemaVersion: 1,
		StartedAt:     time.Date(2026, 8, 15, 4, 0, 0, 0, time.UTC),
	}
	if err := Write(path, want); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var got Metadata
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if got.Address != want.Address || got.PID != want.PID {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}
