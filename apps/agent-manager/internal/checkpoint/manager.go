package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrNotRepository = errors.New("not a Git repository")

type Snapshot struct {
	HeadCommit string
	IndexTree  string
	Tree       string
	Ref        string
}

type Manager struct {
	gitPath string
	// runOnce executes a single git invocation. It is a field (rather than a
	// plain method call) so tests can simulate lock contention without
	// spawning real subprocesses.
	runOnce func(ctx context.Context, repository string, extraEnv []string, args ...string) (string, error)
}

func New() (*Manager, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("find git executable: %w", err)
	}
	manager := &Manager{gitPath: gitPath}
	manager.runOnce = manager.execGit
	return manager, nil
}

func (m *Manager) ValidateRepository(ctx context.Context, workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", ErrNotRepository
	}
	root, err := m.run(ctx, workspace, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotRepository, err)
	}
	root = strings.TrimSpace(root)
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	return filepath.Clean(abs), nil
}

func (m *Manager) Capture(ctx context.Context, repository, sessionID, runID, phase string) (Snapshot, error) {
	if phase != "before" && phase != "after" {
		return Snapshot{}, fmt.Errorf("invalid checkpoint phase %q", phase)
	}
	snapshot, err := m.captureState(ctx, repository)
	if err != nil {
		return Snapshot{}, err
	}
	ref := fmt.Sprintf("refs/maatgen/checkpoints/%s/%s/%s", sessionID, runID, phase)
	if _, err := m.run(ctx, repository, nil, "update-ref", ref, snapshot.Tree); err != nil {
		return Snapshot{}, fmt.Errorf("retain checkpoint ref: %w", err)
	}
	snapshot.Ref = ref
	return snapshot, nil
}

func (m *Manager) captureState(ctx context.Context, repository string) (Snapshot, error) {
	head, err := m.run(ctx, repository, nil, "rev-parse", "HEAD")
	if err != nil {
		// Repository has no commits yet (e.g., just after git init). Use the empty tree hash.
		head = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	}
	indexTree, err := m.run(ctx, repository, nil, "write-tree")
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot index: %w", err)
	}
	temp, err := os.CreateTemp("", "maatgen-index-*")
	if err != nil {
		return Snapshot{}, fmt.Errorf("create temporary index: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		return Snapshot{}, err
	}
	if err := os.Remove(tempPath); err != nil {
		return Snapshot{}, err
	}
	defer os.Remove(tempPath)
	env := []string{"GIT_INDEX_FILE=" + tempPath}
	// read-tree fails for repositories with no commits; in that case we have an empty index already.
	_, _ = m.run(ctx, repository, env, "read-tree", "HEAD")
	if _, err := m.run(ctx, repository, env, "add", "-A", "--", "."); err != nil {
		return Snapshot{}, fmt.Errorf("snapshot working tree: %w", err)
	}
	tree, err := m.run(ctx, repository, env, "write-tree")
	if err != nil {
		return Snapshot{}, fmt.Errorf("write working tree snapshot: %w", err)
	}
	return Snapshot{
		HeadCommit: strings.TrimSpace(head), IndexTree: strings.TrimSpace(indexTree),
		Tree: strings.TrimSpace(tree),
	}, nil
}

func (m *Manager) CaptureTree(ctx context.Context, repository string) (string, error) {
	snapshot, err := m.captureState(ctx, repository)
	if err != nil {
		return "", err
	}
	return snapshot.Tree, nil
}

func (m *Manager) CleanupSession(ctx context.Context, repository, sessionID string) error {
	prefix := "refs/maatgen/checkpoints/" + sessionID + "/"
	output, err := m.run(ctx, repository, nil, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return fmt.Errorf("list checkpoint refs: %w", err)
	}
	for _, ref := range strings.Fields(output) {
		if _, err := m.run(ctx, repository, nil, "update-ref", "-d", ref); err != nil {
			return fmt.Errorf("delete checkpoint ref %q: %w", ref, err)
		}
	}
	return nil
}

func (m *Manager) GitPath() string { return m.gitPath }

// lockRetryDelays bounds how long we wait out a transient "*.lock: File
// exists" failure (e.g. another checkpoint capture, an editor briefly
// touching the index, or a cloud-sync client holding a handle) before
// surfacing the error. We never delete the lock file ourselves: it may
// belong to a genuinely active git process, and removing it could corrupt
// the repository.
var lockRetryDelays = []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, 800 * time.Millisecond, 1500 * time.Millisecond}

func (m *Manager) run(ctx context.Context, repository string, extraEnv []string, args ...string) (string, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		output, err := m.runOnce(ctx, repository, extraEnv, args...)
		if err == nil {
			return output, nil
		}
		lastErr = err
		if attempt >= len(lockRetryDelays) || !isLockContention(err) {
			return "", lastErr
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(lockRetryDelays[attempt]):
		}
	}
}

func isLockContention(err error) bool {
	message := err.Error()
	return strings.Contains(message, ".lock") && strings.Contains(message, "File exists")
}

func (m *Manager) execGit(ctx context.Context, repository string, extraEnv []string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, m.gitPath, append([]string{"-C", repository}, args...)...)
	command.Env = append(os.Environ(), extraEnv...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return stdout.String(), nil
}
