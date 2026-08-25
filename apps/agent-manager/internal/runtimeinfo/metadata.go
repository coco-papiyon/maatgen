package runtimeinfo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Metadata struct {
	PID           int       `json:"pid"`
	Address       string    `json:"address"`
	Version       string    `json:"version"`
	SchemaVersion int       `json:"schemaVersion"`
	StartedAt     time.Time `json:"startedAt"`
}

func Write(path string, metadata Metadata) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime metadata: %w", err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write runtime metadata: %w", err)
	}
	return nil
}
