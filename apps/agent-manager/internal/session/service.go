package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/gitworktree"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

var (
	ErrInvalidRequest   = errors.New("invalid session request")
	ErrUnsupportedAgent = errors.New("unsupported agent")
	ErrRunActive        = errors.New("session has an active run")
	ErrCleanupFailed    = errors.New("worktree cleanup failed")
)

type Store interface {
	CreateSession(ctx context.Context, session protocol.AgentSession) error
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
	PrepareSessionCleanup(ctx context.Context, id string, closedAt time.Time) (protocol.AgentSession, error)
	FinishSessionCleanup(ctx context.Context, id string, status protocol.CleanupStatus, cleanupError *string, updatedAt time.Time) error
}

type WorktreeManager interface {
	Create(ctx context.Context, workspace, destination string) (gitworktree.Worktree, error)
	Remove(ctx context.Context, repository, worktreePath string) error
}

type Service struct {
	store         Store
	worktrees     WorktreeManager
	worktreesRoot string
	now           func() time.Time
	newID         func() (string, error)
}

func New(store Store, worktrees WorktreeManager, worktreesRoot string) *Service {
	return &Service{
		store:         store,
		worktrees:     worktrees,
		worktreesRoot: worktreesRoot,
		now:           time.Now,
		newID:         generateID,
	}
}

func (s *Service) CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error) {
	if request.Agent != protocol.AgentCodex {
		return protocol.AgentSession{}, ErrUnsupportedAgent
	}
	if strings.TrimSpace(request.Workspace) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: workspace is required", ErrInvalidRequest)
	}
	id, err := s.newID()
	if err != nil {
		return protocol.AgentSession{}, fmt.Errorf("generate session ID: %w", err)
	}
	destination := filepath.Join(s.worktreesRoot, id)
	prepared, err := s.worktrees.Create(ctx, request.Workspace, destination)
	if err != nil {
		return protocol.AgentSession{}, err
	}

	created := protocol.AgentSession{
		ID:            id,
		Agent:         request.Agent,
		Workspace:     prepared.Repository,
		Worktree:      prepared.Path,
		BaseCommit:    prepared.BaseCommit,
		Status:        protocol.SessionActive,
		CreatedAt:     s.now().UTC(),
		CleanupStatus: protocol.CleanupNotStarted,
	}
	if err := s.store.CreateSession(ctx, created); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		cleanupErr := s.worktrees.Remove(cleanupCtx, prepared.Repository, prepared.Path)
		if cleanupErr != nil {
			return protocol.AgentSession{}, errors.Join(fmt.Errorf("persist session: %w", err), cleanupErr)
		}
		return protocol.AgentSession{}, fmt.Errorf("persist session: %w", err)
	}
	return created, nil
}

func (s *Service) CloseSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	if strings.TrimSpace(id) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	session, err := s.store.PrepareSessionCleanup(ctx, id, s.now().UTC())
	if errors.Is(err, storage.ErrRunActive) {
		return protocol.AgentSession{}, ErrRunActive
	}
	if err != nil {
		return protocol.AgentSession{}, err
	}
	if session.CleanupStatus == protocol.CleanupCompleted {
		return session, nil
	}

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.worktrees.Remove(cleanupCtx, session.Workspace, session.Worktree); err != nil {
		message := err.Error()
		persistErr := s.store.FinishSessionCleanup(
			cleanupCtx, id, protocol.CleanupFailed, &message, s.now().UTC(),
		)
		if persistErr != nil {
			return protocol.AgentSession{}, errors.Join(fmt.Errorf("%w: %v", ErrCleanupFailed, err), persistErr)
		}
		return protocol.AgentSession{}, fmt.Errorf("%w: %v", ErrCleanupFailed, err)
	}
	if err := s.store.FinishSessionCleanup(cleanupCtx, id, protocol.CleanupCompleted, nil, s.now().UTC()); err != nil {
		return protocol.AgentSession{}, err
	}
	return s.store.GetSession(cleanupCtx, id)
}

func generateID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "session_" + hex.EncodeToString(random), nil
}
