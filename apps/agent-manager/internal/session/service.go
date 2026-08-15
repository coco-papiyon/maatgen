package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

var (
	ErrInvalidRequest   = errors.New("invalid session request")
	ErrUnsupportedAgent = errors.New("unsupported agent")
	ErrRunActive        = errors.New("session has an active run")
	ErrCleanupFailed    = errors.New("checkpoint cleanup failed")
)

type Store interface {
	CreateSession(ctx context.Context, session protocol.AgentSession) error
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
	CloseSession(ctx context.Context, id string, closedAt time.Time) error
}

type RepositoryManager interface {
	ValidateRepository(ctx context.Context, workspace string) (string, error)
	CleanupSession(ctx context.Context, repository, sessionID string) error
}

type Service struct {
	store        Store
	repositories RepositoryManager
	now          func() time.Time
	newID        func() (string, error)
}

func New(store Store, repositories RepositoryManager) *Service {
	return &Service{store: store, repositories: repositories, now: time.Now, newID: generateID}
}

func (s *Service) CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error) {
	if request.Agent != protocol.AgentCodex {
		return protocol.AgentSession{}, ErrUnsupportedAgent
	}
	if strings.TrimSpace(request.Workspace) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: workspace is required", ErrInvalidRequest)
	}
	repository, err := s.repositories.ValidateRepository(ctx, request.Workspace)
	if err != nil {
		return protocol.AgentSession{}, err
	}
	id, err := s.newID()
	if err != nil {
		return protocol.AgentSession{}, fmt.Errorf("generate session ID: %w", err)
	}
	created := protocol.AgentSession{
		ID: id, Agent: request.Agent, Workspace: repository,
		Status: protocol.SessionActive, CreatedAt: s.now().UTC(),
	}
	if err := s.store.CreateSession(ctx, created); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("persist session: %w", err)
	}
	return created, nil
}

func (s *Service) CloseSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	if strings.TrimSpace(id) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	session, err := s.store.GetSession(ctx, id)
	if err != nil {
		return protocol.AgentSession{}, err
	}
	if session.Status == protocol.SessionActive {
		if err := s.store.CloseSession(ctx, id, s.now().UTC()); errors.Is(err, storage.ErrRunActive) {
			return protocol.AgentSession{}, ErrRunActive
		} else if err != nil {
			return protocol.AgentSession{}, err
		}
	}
	if err := s.repositories.CleanupSession(ctx, session.Workspace, session.ID); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("%w: %v", ErrCleanupFailed, err)
	}
	return s.store.GetSession(ctx, id)
}

func generateID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "session_" + hex.EncodeToString(random), nil
}
