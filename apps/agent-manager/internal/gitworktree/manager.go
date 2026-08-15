package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrNotRepository = errors.New("workspace is not a Git repository")
	ErrDirty         = errors.New("workspace has uncommitted changes")
)

type Worktree struct {
	Repository string
	Path       string
	BaseCommit string
}

type Manager struct {
	gitPath string
}

func New() (*Manager, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("find git executable: %w", err)
	}
	return &Manager{gitPath: gitPath}, nil
}

func (m *Manager) Create(ctx context.Context, workspace, destination string) (Worktree, error) {
	repository, err := m.repositoryRoot(ctx, workspace)
	if err != nil {
		return Worktree{}, err
	}

	status, err := m.run(ctx, repository, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return Worktree{}, fmt.Errorf("read Git status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return Worktree{}, ErrDirty
	}

	baseCommit, err := m.run(ctx, repository, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	baseCommit = strings.TrimSpace(baseCommit)

	destination, err = filepath.Abs(destination)
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve worktree destination: %w", err)
	}
	if _, err := os.Stat(destination); err == nil {
		return Worktree{}, fmt.Errorf("worktree destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Worktree{}, fmt.Errorf("inspect worktree destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Worktree{}, fmt.Errorf("create worktree directory: %w", err)
	}

	if _, err := m.run(ctx, repository, "worktree", "add", "--detach", destination, baseCommit); err != nil {
		return Worktree{}, fmt.Errorf("create detached worktree: %w", err)
	}
	return Worktree{Repository: repository, Path: destination, BaseCommit: baseCommit}, nil
}

func (m *Manager) Remove(ctx context.Context, repository, worktreePath string) error {
	if _, err := os.Stat(worktreePath); errors.Is(err, os.ErrNotExist) {
		if _, pruneErr := m.run(ctx, repository, "worktree", "prune"); pruneErr != nil {
			return fmt.Errorf("prune missing worktree: %w", pruneErr)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect worktree: %w", err)
	}
	if _, err := m.run(ctx, repository, "worktree", "remove", "--force", worktreePath); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}

func (m *Manager) repositoryRoot(ctx context.Context, workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", ErrNotRepository
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	root, err := m.run(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotRepository
	}
	root = filepath.Clean(strings.TrimSpace(root))
	return root, nil
}

func (m *Manager) run(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, m.gitPath, append([]string{"-C", directory}, args...)...)
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
