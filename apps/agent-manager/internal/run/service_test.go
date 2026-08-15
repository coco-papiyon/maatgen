package run

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/codex"
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
	service := New(store, adapter)
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
	if err != nil || updatedSession.CodexThreadID == nil || *updatedSession.CodexThreadID != "thread-123" {
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
}

func TestRunServiceRejectsConcurrentRunAndCancels(t *testing.T) {
	store, session := createRunTestStore(t)
	adapter := &fakeAdapter{block: true}
	service := New(store, adapter)
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

func TestRunServiceRefreshesChangeSetAfterSuccessfulRun(t *testing.T) {
	store, session := createRunTestStore(t)
	path := "changed.txt"
	detector := &fakeChangeDetector{changeSet: protocol.ChangeSet{
		SessionID: session.ID,
		Files: []protocol.FileChange{
			{ID: "file-1", NewPath: &path, Kind: protocol.FileAdd, ReviewMode: "hunk", Status: protocol.ReviewPending, Hunks: []protocol.ChangeHunk{}},
		},
	}}
	service := New(store, &fakeAdapter{}, WithChangeDetector(detector))
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
	if detector.session.ID != session.ID {
		t.Fatalf("detector session = %#v", detector.session)
	}
}

func TestRunServiceFailsRunWhenChangeSetRefreshFails(t *testing.T) {
	store, session := createRunTestStore(t)
	detector := &fakeChangeDetector{err: errors.New("git diff failed")}
	service := New(store, &fakeAdapter{}, WithChangeDetector(detector))
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
	service := New(store, &fakeAdapter{runErr: codex.ErrUnavailable})
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
	lines  []agent.Output
	block  bool
	runErr error
}

type fakeChangeDetector struct {
	session   protocol.AgentSession
	changeSet protocol.ChangeSet
	err       error
}

func (f *fakeChangeDetector) Generate(_ context.Context, session protocol.AgentSession) (protocol.ChangeSet, error) {
	f.session = session
	return f.changeSet, f.err
}

var _ ChangeDetector = (*fakeChangeDetector)(nil)

func (*fakeAdapter) Name() protocol.AgentName { return protocol.AgentCodex }

func (*fakeAdapter) Check(context.Context) (agent.Info, error) {
	return agent.Info{Name: protocol.AgentCodex, Path: "fake-codex", Version: "test"}, nil
}

func (f *fakeAdapter) Run(ctx context.Context, _ agent.RunRequest, emit agent.Emitter) (agent.RunResult, error) {
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
	return agent.RunResult{StartedAt: startedAt, FinishedAt: time.Now().UTC(), ExitCode: 0}, nil
}

func createRunTestStore(t *testing.T) (*storesqlite.Store, protocol.AgentSession) {
	t.Helper()
	store, err := storesqlite.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := protocol.AgentSession{
		ID: "session-1", Agent: protocol.AgentCodex, Workspace: t.TempDir(), Worktree: t.TempDir(),
		BaseCommit: "abcdef", Status: protocol.SessionActive, CreatedAt: time.Now().UTC(),
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
