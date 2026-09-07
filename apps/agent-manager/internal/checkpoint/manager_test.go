package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateWorkspaceAcceptsNonGitDirectory(t *testing.T) {
	directory := t.TempDir()
	manager := &Manager{runOnce: func(context.Context, string, []string, ...string) (string, error) {
		return "", errors.New("not a git repository")
	}}
	got, isRepository, err := manager.ValidateWorkspace(context.Background(), directory)
	if err != nil || isRepository {
		t.Fatalf("workspace = %q, repository = %v, err = %v", got, isRepository, err)
	}
	want, _ := filepath.Abs(directory)
	if got != filepath.Clean(want) {
		t.Fatalf("workspace = %q, want %q", got, want)
	}
}

func TestValidateWorkspaceRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{}
	if _, _, err := manager.ValidateWorkspace(context.Background(), file); err == nil {
		t.Fatal("expected non-directory workspace to be rejected")
	}
}

func TestRunRetriesOnLockContentionThenSucceeds(t *testing.T) {
	restore := setFastLockRetryDelays(t)
	defer restore()

	attempts := 0
	manager := &Manager{runOnce: func(context.Context, string, []string, ...string) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("fatal: Unable to create '.git/index.lock': File exists.")
		}
		return "ok", nil
	}}

	output, err := manager.run(context.Background(), "repo", nil, "write-tree")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if output != "ok" {
		t.Fatalf("output = %q", output)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRunGivesUpAfterExhaustingLockRetries(t *testing.T) {
	restore := setFastLockRetryDelays(t)
	defer restore()

	attempts := 0
	lockErr := errors.New("fatal: Unable to create '.git/index.lock': File exists.")
	manager := &Manager{runOnce: func(context.Context, string, []string, ...string) (string, error) {
		attempts++
		return "", lockErr
	}}

	if _, err := manager.run(context.Background(), "repo", nil, "write-tree"); !errors.Is(err, lockErr) {
		t.Fatalf("err = %v, want %v", err, lockErr)
	}
	if want := len(lockRetryDelays) + 1; attempts != want {
		t.Fatalf("attempts = %d, want %d", attempts, want)
	}
}

func TestRunDoesNotRetryUnrelatedErrors(t *testing.T) {
	restore := setFastLockRetryDelays(t)
	defer restore()

	attempts := 0
	unrelatedErr := errors.New("fatal: not a git repository")
	manager := &Manager{runOnce: func(context.Context, string, []string, ...string) (string, error) {
		attempts++
		return "", unrelatedErr
	}}

	if _, err := manager.run(context.Background(), "repo", nil, "write-tree"); !errors.Is(err, unrelatedErr) {
		t.Fatalf("err = %v, want %v", err, unrelatedErr)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry for unrelated errors)", attempts)
	}
}

func TestIsLockContention(t *testing.T) {
	cases := map[string]bool{
		"fatal: Unable to create '.git/index.lock': File exists.":                    true,
		"fatal: Unable to create '.git/refs/heads/main.lock': File exists.":          true,
		"fatal: not a git repository":                                                false,
		"fatal: ambiguous argument 'HEAD': unknown revision or path not in the tree": false,
	}
	for message, want := range cases {
		if got := isLockContention(errors.New(message)); got != want {
			t.Errorf("isLockContention(%q) = %v, want %v", message, got, want)
		}
	}
}

func setFastLockRetryDelays(t *testing.T) func() {
	t.Helper()
	original := lockRetryDelays
	lockRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	return func() { lockRetryDelays = original }
}
