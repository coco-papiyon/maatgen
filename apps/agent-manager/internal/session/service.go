package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
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
	ReopenSession(ctx context.Context, id string) error
	ReplaceSourceStats(ctx context.Context, stats protocol.SourceStats) error
}

type RepositoryManager interface {
	ValidateRepository(ctx context.Context, workspace string) (string, error)
	CleanupSession(ctx context.Context, repository, sessionID string) error
}

// SourceStatsAnalyzer counts source lines per language for a repository. It
// runs once, at Session creation, not on every Run.
type SourceStatsAnalyzer interface {
	Analyze(ctx context.Context, repository string) (protocol.SourceStats, error)
}

type Service struct {
	store        Store
	repositories RepositoryManager
	analyzer     SourceStatsAnalyzer
	now          func() time.Time
	newID        func() (string, error)
}

type Option func(*Service)

func WithSourceStatsAnalyzer(analyzer SourceStatsAnalyzer) Option {
	return func(s *Service) { s.analyzer = analyzer }
}

func New(store Store, repositories RepositoryManager, options ...Option) *Service {
	service := &Service{store: store, repositories: repositories, now: time.Now, newID: generateID}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error) {
	switch request.Agent {
	case protocol.AgentCodex, protocol.AgentClaude, protocol.AgentCopilot:
	default:
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
	if s.analyzer != nil {
		s.recordSourceStats(ctx, created.ID, repository)
	}
	return created, nil
}

// recordSourceStats counts source lines once, at Session creation. It is
// best-effort: a failed count must not fail session creation.
func (s *Service) recordSourceStats(ctx context.Context, sessionID, repository string) {
	stats, err := s.analyzer.Analyze(ctx, repository)
	if err != nil {
		slog.Warn("source stats analysis failed", "session", sessionID, "error", err)
		return
	}
	stats.SessionID = sessionID
	if err := s.store.ReplaceSourceStats(ctx, stats); err != nil {
		slog.Warn("save source stats failed", "session", sessionID, "error", err)
	}
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

func (s *Service) ReopenSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	if strings.TrimSpace(id) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	if err := s.store.ReopenSession(ctx, id); err != nil {
		return protocol.AgentSession{}, err
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
