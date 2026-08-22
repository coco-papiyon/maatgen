//go:build windows

package sourcestats

import (
	"os"
	"testing"
)

func TestToShortPathReturnsExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	short := toShortPath(dir)
	if short == "" {
		t.Fatal("toShortPath returned empty string")
	}
	if _, err := os.Stat(short); err != nil {
		t.Fatalf("short path %q does not resolve: %v", short, err)
	}
}

func TestToShortPathFallsBackOnMissingPath(t *testing.T) {
	missing := "Z:/does-not-exist-maatgen"
	if got := toShortPath(missing); got != missing {
		t.Fatalf("expected fallback to original path, got %q", got)
	}
}
