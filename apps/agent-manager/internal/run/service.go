package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/codex"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/process"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/security"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

var (
	ErrInvalidRequest = errors.New("invalid run request")
	ErrRunActive      = errors.New("session already has an active run")
	ErrRunNotActive   = errors.New("run is not active")
	ErrSessionClosed  = errors.New("session is closed")
	ErrServiceClosed  = errors.New("run service is closed")
)

type Store interface {
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
	UpdateSessionThreadID(ctx context.Context, id, threadID string) error
	CreateRun(ctx context.Context, run protocol.AgentRun) error
	GetRun(ctx context.Context, id string) (protocol.AgentRun, error)
	UpdateRun(ctx context.Context, run protocol.AgentRun) error
	AppendEvent(ctx context.Context, event protocol.SessionEvent) (protocol.SessionEvent, error)
	UpsertRunUsage(ctx context.Context, runID string, usage protocol.TokenUsage, rawJSON json.RawMessage) error
	AppendRedactedRawEvent(ctx context.Context, event storage.RedactedRawEvent) (storage.RedactedRawEvent, error)
	CreateCheckpoint(ctx context.Context, checkpoint protocol.Checkpoint) error
	CompleteCheckpoint(ctx context.Context, id, afterTree, afterRef string, completedAt time.Time) error
	ReplaceChangeSet(ctx context.Context, changeSet protocol.ChangeSet) error
}

type ChangeDetector interface {
	Generate(ctx context.Context, repository string, checkpoint protocol.Checkpoint) (protocol.ChangeSet, error)
}

type CheckpointManager interface {
	Capture(ctx context.Context, repository, sessionID, runID, phase string) (checkpoint.Snapshot, error)
}

type activeRun struct {
	sessionID string
	cancel    context.CancelFunc
}

type Service struct {
	store   Store
	adapter agent.Adapter
	ctx     context.Context
	cancel  context.CancelFunc

	mu              sync.Mutex
	activeByRun     map[string]activeRun
	activeBySession map[string]string
	wg              sync.WaitGroup
	now             func() time.Time
	newID           func(string) (string, error)
	changeDetector  ChangeDetector
	checkpoints     CheckpointManager
}

type Option func(*Service)

func WithChangeDetector(detector ChangeDetector) Option {
	return func(service *Service) {
		service.changeDetector = detector
	}
}

func WithCheckpointManager(manager CheckpointManager) Option {
	return func(service *Service) { service.checkpoints = manager }
}

func New(store Store, adapter agent.Adapter, options ...Option) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		store: store, adapter: adapter, ctx: ctx, cancel: cancel,
		activeByRun: make(map[string]activeRun), activeBySession: make(map[string]string),
		now: time.Now, newID: generateID,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) StartRun(ctx context.Context, sessionID string, request protocol.SendMessageRequest) (protocol.AgentRun, error) {
	message := strings.TrimSpace(request.Message)
	if message == "" {
		return protocol.AgentRun{}, fmt.Errorf("%w: message is required", ErrInvalidRequest)
	}
	if request.Model != nil && strings.TrimSpace(*request.Model) == "" {
		return protocol.AgentRun{}, fmt.Errorf("%w: model must not be empty", ErrInvalidRequest)
	}
	if request.TimeoutSeconds != nil && (*request.TimeoutSeconds < 1 || *request.TimeoutSeconds > 7200) {
		return protocol.AgentRun{}, fmt.Errorf("%w: timeoutSeconds is out of range", ErrInvalidRequest)
	}
	select {
	case <-s.ctx.Done():
		return protocol.AgentRun{}, ErrServiceClosed
	default:
	}

	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return protocol.AgentRun{}, err
	}
	if session.Status != protocol.SessionActive {
		return protocol.AgentRun{}, ErrSessionClosed
	}
	id, err := s.newID("run")
	if err != nil {
		return protocol.AgentRun{}, fmt.Errorf("generate run ID: %w", err)
	}

	s.mu.Lock()
	if _, exists := s.activeBySession[sessionID]; exists {
		s.mu.Unlock()
		return protocol.AgentRun{}, ErrRunActive
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	s.activeBySession[sessionID] = id
	s.activeByRun[id] = activeRun{sessionID: sessionID, cancel: cancel}
	s.mu.Unlock()

	run := protocol.AgentRun{ID: id, SessionID: sessionID, Status: protocol.RunQueued, Prompt: message}
	if err := s.store.CreateRun(ctx, run); err != nil {
		s.release(id, sessionID)
		if errors.Is(err, storage.ErrSessionClosed) {
			return protocol.AgentRun{}, ErrSessionClosed
		}
		return protocol.AgentRun{}, err
	}
	if _, err := s.appendEvent(ctx, sessionID, id, protocol.EventSourceUser, protocol.EventTypeUserPrompt, map[string]any{
		"text": message,
	}); err != nil {
		finishedAt := s.now().UTC()
		run.Status = protocol.RunFailed
		run.FinishedAt = &finishedAt
		_ = s.store.UpdateRun(context.WithoutCancel(ctx), run)
		s.release(id, sessionID)
		return protocol.AgentRun{}, err
	}

	s.wg.Add(1)
	go s.execute(runCtx, session, run, request)
	return run, nil
}

func (s *Service) CancelRun(ctx context.Context, runID string) error {
	s.mu.Lock()
	active, exists := s.activeByRun[runID]
	s.mu.Unlock()
	if exists {
		active.cancel()
		return nil
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if isTerminal(run.Status) {
		return ErrRunNotActive
	}
	return ErrRunNotActive
}

func (s *Service) Close(ctx context.Context) error {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) execute(ctx context.Context, session protocol.AgentSession, run protocol.AgentRun, request protocol.SendMessageRequest) {
	defer s.wg.Done()
	defer s.release(run.ID, session.ID)

	checkpointID := "checkpoint_" + run.ID
	if s.checkpoints == nil {
		s.finishPersistenceFailure(run, errors.New("checkpoint manager is not configured"))
		return
	}
	before, err := s.checkpoints.Capture(context.WithoutCancel(ctx), session.Workspace, session.ID, run.ID, "before")
	if err != nil {
		s.finishPersistenceFailure(run, err)
		return
	}
	checkpointRecord := protocol.Checkpoint{
		ID: checkpointID, SessionID: session.ID, RunID: run.ID, HeadCommit: before.HeadCommit,
		IndexTree: before.IndexTree, BeforeTree: before.Tree, BeforeRef: before.Ref, CreatedAt: s.now().UTC(),
	}
	if err := s.store.CreateCheckpoint(context.WithoutCancel(ctx), checkpointRecord); err != nil {
		s.finishPersistenceFailure(run, err)
		return
	}
	_, _ = s.appendEvent(context.WithoutCancel(ctx), session.ID, run.ID, protocol.EventSourceManager, protocol.EventTypeCheckpointCreated, map[string]any{
		"checkpointId": checkpointID, "beforeTree": before.Tree,
	})

	startedAt := s.now().UTC()
	run.Status = protocol.RunRunning
	run.StartedAt = &startedAt
	if err := s.store.UpdateRun(context.WithoutCancel(ctx), run); err != nil {
		s.finishPersistenceFailure(run, err)
		return
	}
	if _, err := s.appendEvent(context.WithoutCancel(ctx), session.ID, run.ID, protocol.EventSourceManager, protocol.EventTypeRunStarted, map[string]any{}); err != nil {
		s.finishPersistenceFailure(run, err)
		return
	}

	var completedData, failedData json.RawMessage
	model := ""
	if request.Model != nil {
		model = strings.TrimSpace(*request.Model)
	}
	var timeout time.Duration
	if request.TimeoutSeconds != nil {
		timeout = time.Duration(*request.TimeoutSeconds) * time.Second
	}
	result, runErr := s.adapter.Run(ctx, agent.RunRequest{
		Directory: session.Workspace, Prompt: run.Prompt, ThreadID: stringValue(session.CodexThreadID),
		Model: model, Timeout: timeout,
	}, func(output agent.Output) error {
		if output.Stream == agent.OutputStderr {
			return s.persistRaw(ctx, session.ID, run.ID, map[string]any{"stream": "stderr", "line": output.Line})
		}
		parsed := codex.ParseLine(output.Line)
		redactedRaw, err := security.RedactJSON(parsed.RawJSON)
		if err != nil {
			return err
		}
		if err := s.persistRedactedRaw(ctx, session.ID, run.ID, redactedRaw); err != nil {
			return err
		}
		if parsed.ThreadID != "" {
			if err := s.store.UpdateSessionThreadID(ctx, session.ID, parsed.ThreadID); err != nil {
				return err
			}
			session.CodexThreadID = &parsed.ThreadID
		}
		if parsed.Usage != nil {
			if err := s.store.UpsertRunUsage(ctx, run.ID, *parsed.Usage, redactedRaw); err != nil {
				return err
			}
		}
		for _, candidate := range parsed.Events {
			switch candidate.Type {
			case protocol.EventTypeRunStarted:
				continue
			case protocol.EventTypeRunCompleted:
				completedData = candidate.Data
				continue
			case protocol.EventTypeRunFailed:
				failedData = candidate.Data
				continue
			}
			source := protocol.EventSourceCodex
			if parsed.Malformed {
				source = protocol.EventSourceManager
			}
			if _, err := s.appendRawEvent(ctx, session.ID, run.ID, source, candidate.Type, candidate.Data); err != nil {
				return err
			}
		}
		return nil
	})

	finishedAt := result.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = s.now().UTC()
	}
	run.FinishedAt = &finishedAt
	if !result.StartedAt.IsZero() {
		run.StartedAt = &result.StartedAt
	}
	if !result.StartedAt.IsZero() && result.ExitCode >= 0 {
		exitCode := result.ExitCode
		run.ExitCode = &exitCode
	}

	terminalType := protocol.EventTypeRunCompleted
	terminalSource := protocol.EventSourceManager
	terminalData := completedData
	switch {
	case result.Canceled || errors.Is(runErr, process.ErrCanceled):
		run.Status = protocol.RunCancelled
		terminalType = protocol.EventTypeRunCancelled
		terminalData = mustJSON(map[string]any{"reason": "cancelled"})
	case result.TimedOut || errors.Is(runErr, process.ErrTimeout):
		run.Status = protocol.RunFailed
		terminalType = protocol.EventTypeRunFailed
		terminalData = mustJSON(map[string]any{"message": "Codex run timed out", "timeout": true})
	case errors.Is(runErr, codex.ErrUnavailable):
		run.Status = protocol.RunFailed
		terminalType = protocol.EventTypeRunFailed
		terminalData = mustJSON(map[string]any{
			"code": "codex_unavailable", "message": "Codex CLI is not installed or unavailable",
		})
	case runErr != nil || result.ExitCode != 0:
		run.Status = protocol.RunFailed
		terminalType = protocol.EventTypeRunFailed
		terminalData = failedData
		if len(terminalData) == 0 {
			terminalData = mustJSON(map[string]any{"message": "Codex run failed", "exitCode": result.ExitCode})
		}
	default:
		run.Status = protocol.RunCompleted
		if len(terminalData) > 0 {
			terminalSource = protocol.EventSourceCodex
		}
	}
	if terminalType == protocol.EventTypeRunFailed && len(failedData) > 0 && !result.TimedOut {
		terminalSource = protocol.EventSourceCodex
	}
	if len(terminalData) == 0 {
		terminalData = json.RawMessage(`{}`)
	}

	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	after, snapshotErr := s.checkpoints.Capture(persistCtx, session.Workspace, session.ID, run.ID, "after")
	if snapshotErr == nil {
		snapshotErr = s.store.CompleteCheckpoint(persistCtx, checkpointID, after.Tree, after.Ref, s.now().UTC())
		checkpointRecord.AfterTree = &after.Tree
		checkpointRecord.AfterRef = &after.Ref
	}
	if snapshotErr == nil && s.changeDetector != nil {
		changeSet, err := s.changeDetector.Generate(persistCtx, session.Workspace, checkpointRecord)
		if err == nil {
			err = s.store.ReplaceChangeSet(persistCtx, changeSet)
		}
		snapshotErr = err
	}
	if snapshotErr != nil {
		run.Status = protocol.RunFailed
		terminalType = protocol.EventTypeRunFailed
		terminalSource = protocol.EventSourceManager
		terminalData = mustJSON(map[string]any{"message": "capture changes after Codex run", "code": "checkpoint_capture_failed"})
	}
	_ = s.store.UpdateRun(persistCtx, run)
	_, _ = s.appendRawEvent(persistCtx, session.ID, run.ID, terminalSource, terminalType, terminalData)
}

func (s *Service) finishPersistenceFailure(run protocol.AgentRun, _ error) {
	finishedAt := s.now().UTC()
	run.Status = protocol.RunFailed
	run.FinishedAt = &finishedAt
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.store.UpdateRun(ctx, run)
}

func (s *Service) appendEvent(ctx context.Context, sessionID, runID string, source protocol.EventSource, eventType string, data any) (protocol.SessionEvent, error) {
	return s.appendRawEvent(ctx, sessionID, runID, source, eventType, mustJSON(data))
}

func (s *Service) appendRawEvent(ctx context.Context, sessionID, runID string, source protocol.EventSource, eventType string, data json.RawMessage) (protocol.SessionEvent, error) {
	id, err := s.newID("event")
	if err != nil {
		return protocol.SessionEvent{}, err
	}
	return s.store.AppendEvent(ctx, protocol.SessionEvent{
		ID: id, SessionID: sessionID, RunID: &runID, Timestamp: s.now().UTC(),
		SchemaVersion: protocol.SchemaVersion, Source: source, Type: eventType, Data: data,
	})
}

func (s *Service) persistRaw(ctx context.Context, sessionID, runID string, value any) error {
	return s.persistRedactedRaw(ctx, sessionID, runID, mustRedactedJSON(value))
}

func (s *Service) persistRedactedRaw(ctx context.Context, sessionID, runID string, raw json.RawMessage) error {
	_, err := s.store.AppendRedactedRawEvent(ctx, storage.RedactedRawEvent{
		SessionID: sessionID, RunID: &runID, Agent: protocol.AgentCodex,
		RawJSON: raw, CreatedAt: s.now().UTC(),
	})
	return err
}

func (s *Service) release(runID, sessionID string) {
	s.mu.Lock()
	active, exists := s.activeByRun[runID]
	if exists {
		active.cancel()
	}
	delete(s.activeByRun, runID)
	if s.activeBySession[sessionID] == runID {
		delete(s.activeBySession, sessionID)
	}
	s.mu.Unlock()
}

func isTerminal(status protocol.RunStatus) bool {
	return status == protocol.RunCompleted || status == protocol.RunFailed || status == protocol.RunCancelled
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func generateID(prefix string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(random), nil
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func mustRedactedJSON(value any) json.RawMessage {
	redacted, err := security.RedactJSON(mustJSON(value))
	if err != nil {
		panic(err)
	}
	return redacted
}
