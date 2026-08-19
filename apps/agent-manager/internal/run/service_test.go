package run

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/codex"
	claudeadapter "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/claude"
	copilotadapter "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/copilot"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/process"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

func TestRunServicePersistsCodexOutput(t *testing.T) {
	ctx := context.Background()
	store, session := createRunTestStore(t)
	adapter := &fakeAdapter{lines: []agent.Output{
		{Stream: agent.OutputStdout, Line: `{"type":"thread.started","thread_id":"thread-123","api_key":"sk-secret12345"}`},
		{Stream: agent.OutputStderr, Line: `Authorization: Bearer secret-token`},
		{Stream: agent.OutputStdout, Line: `{"type":"item.completed","item":{"id":"item-1","type":"agent_message","text":"Done"}}`},
		{Stream: agent.OutputStdout, Line: `{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":1}}`},
	}}
	service := New(store, adapter, WithCheckpointManager(&fakeCheckpointManager{}))
	defer service.Close(context.Background())

	run, err := service.StartRun(ctx, session.ID, protocol.SendMessageRequest{Message: "Implement it"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	completed := waitForRunStatus(t, store, run.ID, protocol.RunCompleted)
	if completed.ExitCode == nil || *completed.ExitCode != 0 || completed.StartedAt == nil || completed.FinishedAt == nil {
		t.Fatalf("completed run = %#v", completed)
	}

	updatedSession, err := store.GetSession(ctx, session.ID)
	if err != nil || updatedSession.AgentThreadID == nil || *updatedSession.AgentThreadID != "thread-123" {
		t.Fatalf("updated session = %#v, err = %v", updatedSession, err)
	}
	usage, _, err := store.GetRunUsage(ctx, run.ID)
	if err != nil || usage.TotalTokens == nil || *usage.TotalTokens != 13 {
		t.Fatalf("usage = %#v, err = %v", usage, err)
	}
	events, err := store.ListEventsAfter(ctx, session.ID, 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	wantTypes := []string{
		protocol.EventTypeUserPrompt,
		protocol.EventTypeCheckpointCreated,
		protocol.EventTypeRunStarted,
		protocol.EventTypeAssistantMessage,
		protocol.EventTypeUsageReported,
		protocol.EventTypeRunCompleted,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for index, eventType := range wantTypes {
		if events[index].Type != eventType || events[index].Sequence != int64(index+1) {
			t.Fatalf("events = %#v", events)
		}
	}
	rawEvents, err := store.ListRedactedRawEvents(ctx, run.ID, 100)
	if err != nil {
		t.Fatalf("list raw events: %v", err)
	}
	joined := ""
	for _, event := range rawEvents {
		joined += string(event.RawJSON)
	}
	for _, secret := range []string{"sk-secret12345", "secret-token"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("secret %q remains in raw events: %s", secret, joined)
		}
	}
	second, err := service.StartRun(ctx, session.ID, protocol.SendMessageRequest{Message: "Continue"})
	if err != nil {
		t.Fatalf("start continued run: %v", err)
	}
	waitForRunStatus(t, store, second.ID, protocol.RunCompleted)
	if len(adapter.requests) != 2 || adapter.requests[0].Directory != session.Workspace || adapter.requests[1].ThreadID != "thread-123" {
		t.Fatalf("adapter requests = %#v", adapter.requests)
	}
}

func TestRunServicePersistsActualModelReturnedByAppServer(t *testing.T) {
	store, session := createRunTestStore(t)
	adapter := &fakeAdapter{actualModel: "gpt-5.4", lines: []agent.Output{
		{Stream: agent.OutputStdout, Line: `{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"inputTokens":10,"cachedInputTokens":2,"outputTokens":3,"reasoningOutputTokens":1,"totalTokens":13}}}}`},
	}}
	service := New(store, adapter, WithCheckpointManager(&fakeCheckpointManager{}))
	t.Cleanup(func() { _ = service.Close(context.Background()) })

	run, err := service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "Implement it"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, store, run.ID, protocol.RunCompleted)
	usage, _, err := store.GetRunUsage(context.Background(), run.ID)
	if err != nil || usage.ActualModel == nil || *usage.ActualModel != "gpt-5.4" || usage.Model == nil || *usage.Model != "default" {
		t.Fatalf("usage = %#v, err = %v", usage, err)
	}
}

func TestRunServiceSelectsCopilotAdapterAndPersistsThread(t *testing.T) {
	ctx := context.Background()
	store, _ := createRunTestStore(t)
	session := protocol.AgentSession{ID: "session-copilot", Agent: protocol.AgentCopilot, Workspace: t.TempDir(), Status: protocol.SessionActive, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	codexAdapter := &fakeAdapter{}
	copilotAdapter := &fakeAdapter{name: protocol.AgentCopilot, lines: []agent.Output{
		{Stream: agent.OutputStdout, Line: `{"type":"assistant.turn_start","sessionId":"copilot-session-123","data":{"turnId":"1"}}`},
		{Stream: agent.OutputStdout, Line: `{"type":"assistant.message","data":{"messageId":"message-1","content":"Implemented with Copilot."}}`},
		{Stream: agent.OutputStdout, Line: `{"type":"assistant.usage","data":{"model":"gpt-5.4","copilotUsage":{"totalNanoAiu":250000000}}}`},
		{Stream: agent.OutputStdout, Line: `{"type":"assistant.usage","data":{"model":"gpt-5.4","copilotUsage":{"totalNanoAiu":500000000}}}`},
		{Stream: agent.OutputStdout, Line: `{"type":"session.idle","data":{}}`},
	}}
	service := NewMulti(store, []agent.Adapter{codexAdapter, copilotAdapter}, WithCheckpointManager(&fakeCheckpointManager{}), WithChangeDetector(&fakeChangeDetector{}))
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	run, err := service.StartRun(ctx, session.ID, protocol.SendMessageRequest{Message: "implement"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, store, run.ID, protocol.RunCompleted)
	updated, err := store.GetSession(ctx, session.ID)
	if err != nil || updated.AgentThreadID == nil || *updated.AgentThreadID != "copilot-session-123" {
		t.Fatalf("session = %#v, err = %v", updated, err)
	}
	if len(codexAdapter.requests) != 0 || len(copilotAdapter.requests) != 1 {
		t.Fatalf("Codex requests = %d, Copilot requests = %d", len(codexAdapter.requests), len(copilotAdapter.requests))
	}
	events, err := store.ListEventsAfter(ctx, session.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == protocol.EventTypeAssistantMessage && event.Source == protocol.EventSourceCopilot {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %#v", events)
	}
	usage, _, err := store.GetRunUsage(ctx, run.ID)
	if err != nil || usage.Model != nil || usage.ActualModel == nil || *usage.ActualModel != "gpt-5.4" || usage.AICredits == nil || *usage.AICredits != 0.75 || usage.TotalTokens != nil {
		t.Fatalf("usage = %#v, err = %v", usage, err)
	}
}

func TestRunServiceSelectsClaudeAdapterAndKeepsReportedCost(t *testing.T) {
	ctx := context.Background()
	store, _ := createRunTestStore(t)
	session := protocol.AgentSession{ID: "session-claude", Agent: protocol.AgentClaude, Workspace: t.TempDir(), Status: protocol.SessionActive, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	codexAdapter := &fakeAdapter{}
	claudeAdapter := &fakeAdapter{name: protocol.AgentClaude, lines: []agent.Output{
		{Stream: agent.OutputStdout, Line: `{"type":"system","subtype":"init","session_id":"claude-session-123","model":"claude-sonnet-5-20260101"}`},
		{Stream: agent.OutputStdout, Line: `{"type":"assistant","session_id":"claude-session-123","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"Implemented with Claude Code."}]}}`},
		{Stream: agent.OutputStdout, Line: `{"type":"result","subtype":"success","is_error":false,"session_id":"claude-session-123","result":"done","total_cost_usd":0.25,` +
			`"usage":{"input_tokens":100,"cache_creation_input_tokens":50,"cache_read_input_tokens":850,"output_tokens":200},` +
			`"modelUsage":{"claude-sonnet-5-20260101":{"outputTokens":200}}}`},
	}}
	// Pricing is available but must not override the cost Claude Code reported.
	service := NewMulti(store, []agent.Adapter{codexAdapter, claudeAdapter},
		WithCheckpointManager(&fakeCheckpointManager{}), WithChangeDetector(&fakeChangeDetector{}), WithPricingReader(store))
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	run, err := service.StartRun(ctx, session.ID, protocol.SendMessageRequest{Message: "implement"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, store, run.ID, protocol.RunCompleted)
	updated, err := store.GetSession(ctx, session.ID)
	if err != nil || updated.AgentThreadID == nil || *updated.AgentThreadID != "claude-session-123" {
		t.Fatalf("session = %#v, err = %v", updated, err)
	}
	if len(codexAdapter.requests) != 0 || len(claudeAdapter.requests) != 1 {
		t.Fatalf("Codex requests = %d, Claude requests = %d", len(codexAdapter.requests), len(claudeAdapter.requests))
	}
	events, err := store.ListEventsAfter(ctx, session.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == protocol.EventTypeAssistantMessage && event.Source == protocol.EventSourceClaude {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %#v", events)
	}
	usage, _, err := store.GetRunUsage(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens == nil || *usage.InputTokens != 1000 || usage.CachedInputTokens == nil || *usage.CachedInputTokens != 850 {
		t.Fatalf("input usage = %#v", usage)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 1200 || usage.CostUSD == nil || *usage.CostUSD != 0.25 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.Model == nil || *usage.Model != "default" || usage.ActualModel == nil || *usage.ActualModel != "claude-sonnet-5-20260101" {
		t.Fatalf("models = %#v", usage)
	}
}

func TestRunServiceRejectsConcurrentRunAndCancels(t *testing.T) {
	store, session := createRunTestStore(t)
	adapter := &fakeAdapter{block: true}
	service := New(store, adapter, WithCheckpointManager(&fakeCheckpointManager{}))
	defer service.Close(context.Background())

	first, err := service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "First"})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	_, err = service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "Second"})
	if !errors.Is(err, ErrRunActive) {
		t.Fatalf("second run error = %v", err)
	}
	if err := service.CancelRun(context.Background(), first.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	waitForRunStatus(t, store, first.ID, protocol.RunCancelled)
	if err := service.CancelRun(context.Background(), first.ID); !errors.Is(err, ErrRunNotActive) {
		t.Fatalf("second cancel error = %v", err)
	}
}

func TestRunServiceAcceptsFollowUpAsSoonAsTerminalEventIsPublished(t *testing.T) {
	store, session := createRunTestStore(t)
	blockingStore := &terminalBlockingStore{
		Store: store, terminalEntered: make(chan struct{}), releaseTerminal: make(chan struct{}),
	}
	service := New(blockingStore, &fakeAdapter{}, WithCheckpointManager(&fakeCheckpointManager{}))
	defer service.Close(context.Background())

	first, err := service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "First"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blockingStore.terminalEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal event was not reached")
	}
	second, err := service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "Follow up"})
	if err != nil {
		t.Fatalf("follow-up run was rejected after completion: %v", err)
	}
	close(blockingStore.releaseTerminal)
	waitForRunStatus(t, store, first.ID, protocol.RunCompleted)
	waitForRunStatus(t, store, second.ID, protocol.RunCompleted)
}

func TestRunServiceRefreshesChangeSetAfterSuccessfulRun(t *testing.T) {
	store, session := createRunTestStore(t)
	path := "changed.txt"
	detector := &fakeChangeDetector{changeSet: protocol.ChangeSet{
		SessionID: session.ID,
		Files: []protocol.FileChange{
			{ID: "file-1", NewPath: &path, Kind: protocol.FileAdd, RestoreMode: "file", Status: protocol.RestoreChanged, Hunks: []protocol.ChangeHunk{}},
		},
	}}
	service := New(store, &fakeAdapter{}, WithCheckpointManager(&fakeCheckpointManager{}), WithChangeDetector(detector))
	defer service.Close(context.Background())

	run, err := service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "Change a file"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, store, run.ID, protocol.RunCompleted)
	got, err := store.GetChangeSet(context.Background(), session.ID)
	if err != nil || len(got.Files) != 1 || got.Files[0].ID != "file-1" {
		t.Fatalf("change set = %#v, err = %v", got, err)
	}
	if detector.repository != session.Workspace || detector.checkpoint.SessionID != session.ID {
		t.Fatalf("detector arguments = %q, %#v", detector.repository, detector.checkpoint)
	}
}

func TestRunServiceFailsRunWhenChangeSetRefreshFails(t *testing.T) {
	store, session := createRunTestStore(t)
	detector := &fakeChangeDetector{err: errors.New("git diff failed")}
	service := New(store, &fakeAdapter{}, WithCheckpointManager(&fakeCheckpointManager{}), WithChangeDetector(detector))
	defer service.Close(context.Background())

	run, err := service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "Change a file"})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForRunStatus(t, store, run.ID, protocol.RunFailed)
	if completed.ExitCode == nil || *completed.ExitCode != 0 {
		t.Fatalf("failed post-processing run = %#v", completed)
	}
	events, err := store.ListEventsAfter(context.Background(), session.ID, 0, 10)
	if err != nil || len(events) == 0 || events[len(events)-1].Type != protocol.EventTypeRunFailed {
		t.Fatalf("events = %#v, err = %v", events, err)
	}
}

func TestRunServiceReportsUnavailableCodex(t *testing.T) {
	store, session := createRunTestStore(t)
	service := New(store, &fakeAdapter{runErr: codex.ErrUnavailable}, WithCheckpointManager(&fakeCheckpointManager{}))
	defer service.Close(context.Background())

	run, err := service.StartRun(context.Background(), session.ID, protocol.SendMessageRequest{Message: "Do work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, store, run.ID, protocol.RunFailed)
	events, err := store.ListEventsAfter(context.Background(), session.ID, 0, 10)
	if err != nil || len(events) == 0 {
		t.Fatalf("events = %#v, err = %v", events, err)
	}
	terminal := events[len(events)-1]
	var data struct {
		Code string `json:"code"`
	}
	if terminal.Type != protocol.EventTypeRunFailed || json.Unmarshal(terminal.Data, &data) != nil || data.Code != "codex_unavailable" {
		t.Fatalf("terminal event = %#v", terminal)
	}
}

type fakeAdapter struct {
	name        protocol.AgentName
	lines       []agent.Output
	block       bool
	runErr      error
	actualModel string
	requests    []agent.RunRequest
}

type terminalBlockingStore struct {
	*storesqlite.Store
	mu              sync.Mutex
	blocked         bool
	terminalEntered chan struct{}
	releaseTerminal chan struct{}
}

func (s *terminalBlockingStore) AppendEvent(ctx context.Context, event protocol.SessionEvent) (protocol.SessionEvent, error) {
	shouldBlock := false
	if event.Type == protocol.EventTypeRunCompleted || event.Type == protocol.EventTypeRunFailed || event.Type == protocol.EventTypeRunCancelled {
		s.mu.Lock()
		if !s.blocked {
			s.blocked = true
			shouldBlock = true
		}
		s.mu.Unlock()
	}
	if shouldBlock {
		close(s.terminalEntered)
		select {
		case <-s.releaseTerminal:
		case <-ctx.Done():
			return protocol.SessionEvent{}, ctx.Err()
		}
	}
	return s.Store.AppendEvent(ctx, event)
}

type fakeChangeDetector struct {
	repository string
	checkpoint protocol.Checkpoint
	changeSet  protocol.ChangeSet
	err        error
}

func (f *fakeChangeDetector) Generate(_ context.Context, repository string, checkpoint protocol.Checkpoint) (protocol.ChangeSet, error) {
	f.repository = repository
	f.checkpoint = checkpoint
	f.changeSet.SessionID = checkpoint.SessionID
	f.changeSet.RunID = checkpoint.RunID
	f.changeSet.CheckpointID = checkpoint.ID
	f.changeSet.BeforeTree = checkpoint.BeforeTree
	if checkpoint.AfterTree != nil {
		f.changeSet.AfterTree = *checkpoint.AfterTree
	}
	return f.changeSet, f.err
}

type fakeCheckpointManager struct{}

func (*fakeCheckpointManager) Capture(_ context.Context, _ string, sessionID, runID, phase string) (checkpoint.Snapshot, error) {
	return checkpoint.Snapshot{
		HeadCommit: "head-tree", IndexTree: "index-tree", Tree: phase + "-tree",
		Ref: "refs/maatgen/checkpoints/" + sessionID + "/" + runID + "/" + phase,
	}, nil
}

var _ ChangeDetector = (*fakeChangeDetector)(nil)

func (f *fakeAdapter) Name() protocol.AgentName {
	if f.name != "" {
		return f.name
	}
	return protocol.AgentCodex
}

func (*fakeAdapter) Check(context.Context) (agent.Info, error) {
	return agent.Info{Name: protocol.AgentCodex, Path: "fake-codex", Version: "test"}, nil
}

func (f *fakeAdapter) ParseLine(line string) agent.ParsedLine {
	switch f.Name() {
	case protocol.AgentCopilot:
		return copilotadapter.ParseLine(line)
	case protocol.AgentClaude:
		return claudeadapter.ParseLine(line)
	default:
		return codex.ParseLine(line)
	}
}

func (f *fakeAdapter) Run(ctx context.Context, request agent.RunRequest, emit agent.Emitter) (agent.RunResult, error) {
	f.requests = append(f.requests, request)
	startedAt := time.Now().UTC()
	for _, output := range f.lines {
		if err := emit(output); err != nil {
			return agent.RunResult{StartedAt: startedAt, FinishedAt: time.Now().UTC(), ExitCode: 1}, err
		}
	}
	if f.block {
		<-ctx.Done()
		return agent.RunResult{
			StartedAt: startedAt, FinishedAt: time.Now().UTC(), ExitCode: 1, Canceled: true,
		}, process.ErrCanceled
	}
	if f.runErr != nil {
		return agent.RunResult{StartedAt: startedAt, FinishedAt: time.Now().UTC(), ExitCode: -1}, f.runErr
	}
	return agent.RunResult{StartedAt: startedAt, FinishedAt: time.Now().UTC(), ExitCode: 0, ActualModel: f.actualModel}, nil
}

func createRunTestStore(t *testing.T) (*storesqlite.Store, protocol.AgentSession) {
	t.Helper()
	store, err := storesqlite.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := protocol.AgentSession{
		ID: "session-1", Agent: protocol.AgentCodex, Workspace: t.TempDir(),
		Status: protocol.SessionActive, CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store, session
}

func waitForRunStatus(t *testing.T, store *storesqlite.Store, runID string, want protocol.RunStatus) protocol.AgentRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.GetRun(context.Background(), runID)
		if err == nil && run.Status == want {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, err := store.GetRun(context.Background(), runID)
	t.Fatalf("run did not reach %s: %#v, err = %v", want, run, err)
	return protocol.AgentRun{}
}

var _ agent.Adapter = (*fakeAdapter)(nil)
