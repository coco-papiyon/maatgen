package runtimeinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateTokenAndWriteMetadata(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if len(token) < 40 {
		t.Fatalf("token is unexpectedly short: %d", len(token))
	}

	path := filepath.Join(t.TempDir(), "runtime", "manager.json")
	want := Metadata{
		PID:           123,
		Address:       "127.0.0.1:3100",
		AuthToken:     token,
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
	if got.AuthToken != want.AuthToken || got.Address != want.Address {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}
