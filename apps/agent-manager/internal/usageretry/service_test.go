package usageretry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
)

func TestTickLeavesRunPendingWhileUsageExhausted(t *testing.T) {
	pendingAt := time.Now().UTC()
	store := newFakeStore()
	store.runs["run-1"] = protocol.AgentRun{ID: "run-1", SessionID: "session-1", Status: protocol.RunFailed, UsageLimitRetryPendingAt: &pendingAt}
	store.sessions["session-1"] = protocol.AgentSession{ID: "session-1", Agent: protocol.AgentClaude, Workspace: "/repo", Status: protocol.SessionActive}
	starter := &fakeRunStarter{}
	usage := &fakeProviderUsage{usage: protocol.ProviderUsage{Windows: []protocol.ProviderUsageWindow{{Name: "session", RemainingPercent: 0}}}}

	service := New(store, starter, usage)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(starter.calls) != 0 {
		t.Fatalf("expected no retry run to be started, got %#v", starter.calls)
	}
	if store.runs["run-1"].UsageLimitRetryPendingAt == nil {
		t.Fatal("expected the pending flag to remain set while usage is exhausted")
	}
}

func TestTickResumesSessionWhenUsageRecovers(t *testing.T) {
	pendingAt := time.Now().UTC()
	store := newFakeStore()
	store.runs["run-1"] = protocol.AgentRun{ID: "run-1", SessionID: "session-1", Status: protocol.RunFailed, UsageLimitRetryPendingAt: &pendingAt}
	store.sessions["session-1"] = protocol.AgentSession{ID: "session-1", Agent: protocol.AgentClaude, Workspace: "/repo", Status: protocol.SessionActive}
	starter := &fakeRunStarter{}
	usage := &fakeProviderUsage{usage: protocol.ProviderUsage{Windows: []protocol.ProviderUsageWindow{{Name: "session", RemainingPercent: 50}}}}

	service := New(store, starter, usage)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(starter.calls) != 1 {
		t.Fatalf("expected exactly one retry run to be started, got %#v", starter.calls)
	}
	call := starter.calls[0]
	if call.sessionID != "session-1" {
		t.Fatalf("retry run started against wrong session: %#v", call)
	}
	if call.request.Message != continuationPrompt {
		t.Fatalf("retry run prompt = %q, want continuation prompt", call.request.Message)
	}
	if call.request.AutoRetryOfRunID == nil || *call.request.AutoRetryOfRunID != "run-1" {
		t.Fatalf("retry run AutoRetryOfRunID = %#v, want run-1", call.request.AutoRetryOfRunID)
	}
	if store.runs["run-1"].UsageLimitRetryPendingAt != nil {
		t.Fatal("expected the pending flag to be cleared after a successful retry")
	}
}

func TestTickTreatsUsageFetchFailureAsRecovered(t *testing.T) {
	pendingAt := time.Now().UTC()
	store := newFakeStore()
	store.runs["run-1"] = protocol.AgentRun{ID: "run-1", SessionID: "session-1", Status: protocol.RunFailed, UsageLimitRetryPendingAt: &pendingAt}
	store.sessions["session-1"] = protocol.AgentSession{ID: "session-1", Agent: protocol.AgentClaude, Workspace: "/repo", Status: protocol.SessionActive}
	starter := &fakeRunStarter{}
	usage := &fakeProviderUsage{err: errors.New("usage command failed")}

	service := New(store, starter, usage)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(starter.calls) != 1 {
		t.Fatalf("expected a usage fetch failure to be treated as recovered, got %#v", starter.calls)
	}
}

func TestTickGivesUpWhenSessionIsClosed(t *testing.T) {
	pendingAt := time.Now().UTC()
	store := newFakeStore()
	store.runs["run-1"] = protocol.AgentRun{ID: "run-1", SessionID: "session-1", Status: protocol.RunFailed, UsageLimitRetryPendingAt: &pendingAt}
	store.sessions["session-1"] = protocol.AgentSession{ID: "session-1", Agent: protocol.AgentClaude, Workspace: "/repo", Status: protocol.SessionClosed}
	starter := &fakeRunStarter{}
	usage := &fakeProviderUsage{usage: protocol.ProviderUsage{Windows: []protocol.ProviderUsageWindow{{Name: "session", RemainingPercent: 50}}}}

	service := New(store, starter, usage)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(starter.calls) != 0 {
		t.Fatalf("expected no retry run for a closed session, got %#v", starter.calls)
	}
	if store.runs["run-1"].UsageLimitRetryPendingAt != nil {
		t.Fatal("expected the pending flag to be cleared when the session is closed")
	}
}

func TestTickLeavesRunPendingWhenRepositoryIsBusy(t *testing.T) {
	pendingAt := time.Now().UTC()
	store := newFakeStore()
	store.runs["run-1"] = protocol.AgentRun{ID: "run-1", SessionID: "session-1", Status: protocol.RunFailed, UsageLimitRetryPendingAt: &pendingAt}
	store.sessions["session-1"] = protocol.AgentSession{ID: "session-1", Agent: protocol.AgentClaude, Workspace: "/repo", Status: protocol.SessionActive}
	starter := &fakeRunStarter{err: runservice.ErrRepositoryBusy}
	usage := &fakeProviderUsage{usage: protocol.ProviderUsage{Windows: []protocol.ProviderUsageWindow{{Name: "session", RemainingPercent: 50}}}}

	service := New(store, starter, usage)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if store.runs["run-1"].UsageLimitRetryPendingAt == nil {
		t.Fatal("expected the pending flag to remain set while the repository lock is held elsewhere")
	}
}

func TestTickClearsPendingWhenSessionAlreadyHasANewerRun(t *testing.T) {
	pendingAt := time.Now().UTC()
	store := newFakeStore()
	store.runs["run-1"] = protocol.AgentRun{ID: "run-1", SessionID: "session-1", Status: protocol.RunFailed, UsageLimitRetryPendingAt: &pendingAt}
	store.sessions["session-1"] = protocol.AgentSession{ID: "session-1", Agent: protocol.AgentClaude, Workspace: "/repo", Status: protocol.SessionActive}
	starter := &fakeRunStarter{err: runservice.ErrRunActive}
	usage := &fakeProviderUsage{usage: protocol.ProviderUsage{Windows: []protocol.ProviderUsageWindow{{Name: "session", RemainingPercent: 50}}}}

	service := New(store, starter, usage)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if store.runs["run-1"].UsageLimitRetryPendingAt != nil {
		t.Fatal("expected the pending flag to be cleared once another run already covers the session")
	}
}

type fakeStore struct {
	runs     map[string]protocol.AgentRun
	sessions map[string]protocol.AgentSession
}

func newFakeStore() *fakeStore {
	return &fakeStore{runs: make(map[string]protocol.AgentRun), sessions: make(map[string]protocol.AgentSession)}
}

func (f *fakeStore) ListRunsPendingUsageLimitRetry(ctx context.Context) ([]protocol.AgentRun, error) {
	var pending []protocol.AgentRun
	for _, run := range f.runs {
		if run.UsageLimitRetryPendingAt != nil {
			pending = append(pending, run)
		}
	}
	return pending, nil
}

func (f *fakeStore) GetSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	session, ok := f.sessions[id]
	if !ok {
		return protocol.AgentSession{}, errors.New("session not found")
	}
	return session, nil
}

func (f *fakeStore) UpdateRun(ctx context.Context, run protocol.AgentRun) error {
	f.runs[run.ID] = run
	return nil
}

type runStartCall struct {
	sessionID string
	request   protocol.SendMessageRequest
}

type fakeRunStarter struct {
	err   error
	calls []runStartCall
}

func (f *fakeRunStarter) StartRun(ctx context.Context, sessionID string, request protocol.SendMessageRequest) (protocol.AgentRun, error) {
	f.calls = append(f.calls, runStartCall{sessionID: sessionID, request: request})
	if f.err != nil {
		return protocol.AgentRun{}, f.err
	}
	return protocol.AgentRun{ID: "retry-run", SessionID: sessionID, Status: protocol.RunQueued}, nil
}

type fakeProviderUsage struct {
	usage protocol.ProviderUsage
	err   error
}

func (f *fakeProviderUsage) GetProviderUsage(ctx context.Context, provider protocol.AgentName, directory string) (protocol.ProviderUsage, error) {
	if f.err != nil {
		return protocol.ProviderUsage{}, f.err
	}
	return f.usage, nil
}
