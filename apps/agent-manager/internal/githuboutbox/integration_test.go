package githuboutbox

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

// noopAdapter completes a Run immediately without emitting any output; it
// stands in for a real provider CLI so this test exercises the real
// session.Service and run.Service wiring without invoking one.
type noopAdapter struct{}

func (noopAdapter) Name() protocol.AgentName                  { return protocol.AgentCodex }
func (noopAdapter) Check(context.Context) (agent.Info, error) { return agent.Info{}, nil }
func (noopAdapter) ParseLine(string) agent.ParsedLine         { return agent.ParsedLine{} }
func (noopAdapter) Run(_ context.Context, _ agent.RunRequest, _ agent.Emitter) (agent.RunResult, error) {
	now := time.Now().UTC()
	return agent.RunResult{StartedAt: now, FinishedAt: now, ExitCode: 0}, nil
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	run := func(args ...string) {
		cmd := exec.Command(gitPath, append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	run("init")
	run("config", "user.email", "maatgen@example.invalid")
	run("config", "user.name", "Maatgen Test")
	run("commit", "--allow-empty", "-m", "initial")
	return repo
}

// TestDispatcherEndToEndWithRealSessionAndRunServices wires the real
// session.Service and run.Service (ADR-007 section 4: an automated Run must
// be an ordinary Run, not a separate execution path) to a real SQLite
// store, and confirms a queued Outbox event is turned into a real Session,
// a real Run that runs to completion, and a monitor event that ends up
// "completed" with the Session/Run IDs attached.
func TestDispatcherEndToEndWithRealSessionAndRunServices(t *testing.T) {
	ctx := context.Background()
	repository := initGitRepo(t)

	store, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	checkpointManager, err := checkpoint.New()
	if err != nil {
		t.Fatalf("checkpoint.New: %v", err)
	}
	sessions := session.New(store, checkpointManager)

	monitor := protocol.GitHubRepositoryMonitor{
		Repository: repository, Host: "github.com", Owner: "octo-org", Name: "example",
		RemoteName: "origin", Enabled: true, PollIntervalSeconds: 300, CoalesceQueueLimit: 20,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create repository monitor: %v", err)
	}
	rule := protocol.GitHubTriggerRule{
		ID: "rule-1", Repository: repository, Name: "Design when Ready", Enabled: true,
		EventKinds:        []protocol.GitHubItemKind{protocol.GitHubItemIssue},
		PromptTemplate:    "Design {{.Title}} (#{{.Number}})",
		Provider:          protocol.AgentCodex,
		ConcurrencyPolicy: protocol.GitHubConcurrencyCoalesce,
		Priority:          protocol.GitHubPriorityMedium,
		CreatedAt:         time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create trigger rule: %v", err)
	}
	ruleID := rule.ID
	event := protocol.GitHubMonitorEvent{
		ID: "event-1", Repository: repository, RuleID: &ruleID, Kind: protocol.GitHubItemIssue, Number: 42,
		Action: "opened", AfterStateHash: "hash-1", Status: protocol.GitHubMonitorEventQueued,
		ItemSnapshot: protocol.GitHubItem{Kind: protocol.GitHubItemIssue, Number: 42, Title: "Wire up the widget", URL: "https://github.com/octo-org/example/issues/42"},
		CreatedAt:    time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if inserted, err := store.InsertMonitorEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("insert monitor event: inserted=%v err=%v", inserted, err)
	}

	var dispatcher *Dispatcher
	runs := runservice.New(store, noopAdapter{}, runservice.WithCheckpointManager(checkpointManager),
		runservice.WithTerminalObserver(func(run protocol.AgentRun) { dispatcher.ObserveRunTerminal(run) }))
	defer runs.Close(context.Background())
	dispatcher = NewDispatcher(store, sessions, runs)

	if err := dispatcher.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var got protocol.GitHubMonitorEvent
	for time.Now().Before(deadline) {
		got, err = store.GetMonitorEvent(ctx, event.ID)
		if err != nil {
			t.Fatalf("get monitor event: %v", err)
		}
		if got.Status == protocol.GitHubMonitorEventCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Status != protocol.GitHubMonitorEventCompleted {
		t.Fatalf("event did not reach completed: %#v", got)
	}
	if got.SessionID == nil || got.RunID == nil {
		t.Fatalf("event = %#v, want session and run ids attached", got)
	}

	gotSession, err := store.GetSession(ctx, *got.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if gotSession.Workspace != repository || gotSession.Agent != protocol.AgentCodex {
		t.Fatalf("session = %#v", gotSession)
	}
	gotRun, err := store.GetRun(ctx, *got.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.Status != protocol.RunCompleted {
		t.Fatalf("run.Status = %v, want completed", gotRun.Status)
	}
	if gotRun.Prompt != "Design Wire up the widget (#42)" {
		t.Fatalf("run.Prompt = %q", gotRun.Prompt)
	}
}
