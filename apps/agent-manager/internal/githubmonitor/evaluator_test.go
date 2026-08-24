package githubmonitor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testRepositoryMonitor(repository string, lastSyncedAt *time.Time) protocol.GitHubRepositoryMonitor {
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	return protocol.GitHubRepositoryMonitor{
		Repository:          repository,
		Host:                "github.com",
		Owner:               "octo-org",
		Name:                "example",
		RemoteName:          "origin",
		Enabled:             true,
		PollIntervalSeconds: 300,
		CoalesceQueueLimit:  20,
		LastSyncedAt:        lastSyncedAt,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func createReadyRule(t *testing.T, store *sqlite.Store, repository string) protocol.GitHubTriggerRule {
	t.Helper()
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	rule := protocol.GitHubTriggerRule{
		ID:             "rule-ready",
		Repository:     repository,
		Name:           "Design when Ready",
		Enabled:        true,
		EventKinds:     []protocol.GitHubItemKind{protocol.GitHubItemIssue},
		PromptTemplate: "Design {{.Title}}",
		Provider:       protocol.AgentCodex,
		Filters: protocol.GitHubMonitorFilters{
			Project: &protocol.GitHubProjectFilterCondition{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"},
		},
		ConcurrencyPolicy: protocol.GitHubConcurrencyCoalesce,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := store.CreateTriggerRule(context.Background(), rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	return rule
}

// TestEvaluateItemFirstSyncNeverFires is the ADR-007 example almost
// verbatim: an Issue that is already Status=Ready the very first time a
// repository is monitored must not retroactively fire, even though the
// rule's condition is already satisfied. Only a later, genuine change
// should fire it.
func TestEvaluateItemFirstSyncNeverFires(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "C:/workspace/example"
	monitor := testRepositoryMonitor(repository, nil) // LastSyncedAt == nil: first-ever sync
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	createReadyRule(t, store, repository)

	item := baseItem()
	item.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}

	events, err := NewEvaluator(store).EvaluateItem(ctx, monitor, item)
	if err != nil {
		t.Fatalf("EvaluateItem: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none fired during the monitor's first sync", events)
	}

	observed, err := store.GetObservedItem(ctx, repository, item.Kind, item.Number)
	if err != nil {
		t.Fatalf("expected a baseline observed item to be recorded: %v", err)
	}
	if observed.StateHash != HashItem(item) {
		t.Fatalf("observed baseline does not match the fetched item")
	}
}

// TestEvaluateItemFiresOnceProjectBecomesReady exercises the full flow: a
// second poll after the monitor's first sync, where the same Issue's
// Status genuinely transitions to Ready, must fire exactly once.
func TestEvaluateItemFiresOnceProjectBecomesReady(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "C:/workspace/example"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	rule := createReadyRule(t, store, repository)

	notReady := baseItem()
	notReady.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Todo"}}
	if _, err := NewEvaluator(store).EvaluateItem(ctx, monitor, notReady); err != nil {
		t.Fatalf("EvaluateItem (baseline): %v", err)
	}

	ready := baseItem()
	ready.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}
	events, err := NewEvaluator(store).EvaluateItem(ctx, monitor, ready)
	if err != nil {
		t.Fatalf("EvaluateItem (ready): %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want exactly one match", events)
	}
	if events[0].RuleID == nil || *events[0].RuleID != rule.ID {
		t.Fatalf("event.RuleID = %#v, want %q", events[0].RuleID, rule.ID)
	}
	if events[0].Status != protocol.GitHubMonitorEventQueued {
		t.Fatalf("event.Status = %v, want queued", events[0].Status)
	}
	if events[0].DeliveryKey == nil || *events[0].DeliveryKey == "" {
		t.Fatalf("expected a non-empty delivery key")
	}
}

// TestEvaluateItemProjectGapThenAvailable covers the "Project欠損" case:
// while Project data is unavailable, a Project-gated rule must not fire
// even though something else about the item changed (so a diff *is*
// detected); once Project data comes back with a matching value on a
// later poll, it should fire then.
func TestEvaluateItemProjectGapThenAvailable(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "C:/workspace/example"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	createReadyRule(t, store, repository)

	baseline := baseItem()
	baseline.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Todo"}}
	if _, err := NewEvaluator(store).EvaluateItem(ctx, monitor, baseline); err != nil {
		t.Fatalf("EvaluateItem (baseline): %v", err)
	}

	// Something else changes (title), but this poll's Projects fetch fails.
	gap := baseItem()
	gap.Title = "Updated title while Projects API is down"
	gap.ProjectsError = "GraphQL error: Resource not accessible"
	events, err := NewEvaluator(store).EvaluateItem(ctx, monitor, gap)
	if err != nil {
		t.Fatalf("EvaluateItem (gap): %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none while Project data is unavailable", events)
	}

	// Projects recovers with Status = Ready.
	recovered := baseItem()
	recovered.Title = gap.Title
	recovered.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}
	events, err = NewEvaluator(store).EvaluateItem(ctx, monitor, recovered)
	if err != nil {
		t.Fatalf("EvaluateItem (recovered): %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want exactly one match once Projects recovers with Status=Ready", events)
	}
}

// TestEvaluateItemUnchangedProducesNoEvents exercises the "重複" /
// "同一イベントの再取得" requirement at the Evaluator level: polling and
// observing the exact same item state twice in a row must not re-fire a
// rule the second time, since nothing changed to evaluate. (The lower-level
// delivery-key uniqueness mechanism this ultimately relies on is tested
// directly in internal/storage/sqlite.)
func TestEvaluateItemUnchangedProducesNoEvents(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "C:/workspace/example"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	createReadyRule(t, store, repository)

	item := baseItem()
	evaluator := NewEvaluator(store)
	if _, err := evaluator.EvaluateItem(ctx, monitor, item); err != nil {
		t.Fatalf("first EvaluateItem: %v", err)
	}
	events, err := evaluator.EvaluateItem(ctx, monitor, item)
	if err != nil {
		t.Fatalf("second EvaluateItem: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none: item did not change between polls", events)
	}
}

func TestEvaluateItemFiresOnceWhenRuleIsAddedAfterIssueWasObserved(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "C:/workspace/example"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	item := baseItem()
	item.Assignees = []protocol.GitHubUser{{Login: "coco-papiyon"}}
	evaluator := NewEvaluator(store)
	evaluator.now = func() time.Time { return time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC) }
	if _, err := evaluator.EvaluateItem(ctx, monitor, item); err != nil {
		t.Fatalf("EvaluateItem (baseline): %v", err)
	}

	ruleTime := time.Date(2026, 8, 24, 11, 0, 0, 0, time.UTC)
	rule := protocol.GitHubTriggerRule{
		ID: "rule-added-after-issue", Repository: repository, Name: "Implement assigned issue", Enabled: true,
		EventKinds:     []protocol.GitHubItemKind{protocol.GitHubItemIssue},
		Filters:        protocol.GitHubMonitorFilters{Assignees: []string{"coco-papiyon"}},
		PromptTemplate: "Implement issue #{{.Number}}", Provider: protocol.AgentCodex,
		ConcurrencyPolicy: protocol.GitHubConcurrencyCoalesce, CreatedAt: ruleTime, UpdatedAt: ruleTime,
	}
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	evaluator.now = func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }
	events, err := evaluator.EvaluateItem(ctx, monitor, item)
	if err != nil {
		t.Fatalf("EvaluateItem (new rule): %v", err)
	}
	if len(events) != 1 || events[0].RuleID == nil || *events[0].RuleID != rule.ID {
		t.Fatalf("events = %#v, want one event for the newly added rule", events)
	}
	if events[0].Action != "opened" {
		t.Fatalf("event.Action = %q, want opened for the current open issue", events[0].Action)
	}

	evaluator.now = func() time.Time { return time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC) }
	events, err = evaluator.EvaluateItem(ctx, monitor, item)
	if err != nil {
		t.Fatalf("EvaluateItem (same rule version): %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want no duplicate for the same rule version", events)
	}
}

func TestEvaluateItemDoesNotFireAgainWhenMatchingRuleIsUpdated(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "C:/workspace/example"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	item := baseItem()
	item.Assignees = []protocol.GitHubUser{{Login: "coco-papiyon"}}
	rule := protocol.GitHubTriggerRule{
		ID: "rule-updated", Repository: repository, Name: "Implement assigned issue", Enabled: true,
		EventKinds:     []protocol.GitHubItemKind{protocol.GitHubItemIssue},
		Filters:        protocol.GitHubMonitorFilters{Assignees: []string{"coco-papiyon"}},
		PromptTemplate: "Implement issue #{{.Number}}", Provider: protocol.AgentCodex,
		ConcurrencyPolicy: protocol.GitHubConcurrencyCoalesce,
		CreatedAt:         time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC),
	}
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	evaluator := NewEvaluator(store)
	evaluator.now = func() time.Time { return time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC) }
	first, err := evaluator.EvaluateItem(ctx, monitor, item)
	if err != nil || len(first) != 1 {
		t.Fatalf("EvaluateItem (initial rule): events=%#v err=%v", first, err)
	}

	rule.PromptTemplate = "Implement and test issue #{{.Number}}"
	rule.UpdatedAt = time.Date(2026, 8, 24, 11, 0, 0, 0, time.UTC)
	if err := store.UpdateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("update rule: %v", err)
	}
	evaluator.now = func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }
	second, err := evaluator.EvaluateItem(ctx, monitor, item)
	if err != nil || len(second) != 0 {
		t.Fatalf("EvaluateItem (updated rule): events=%#v err=%v, want no second event for the same rule and item", second, err)
	}
}

func TestEvaluateItemFiresOnlyOnceAcrossActionsForSameRuleAndNumber(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "C:/workspace/example"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	rule := createReadyRule(t, store, repository)
	rule.Filters = protocol.GitHubMonitorFilters{}
	if err := store.UpdateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("update rule: %v", err)
	}

	evaluator := NewEvaluator(store)
	opened := baseItem()
	first, err := evaluator.EvaluateItem(ctx, monitor, opened)
	if err != nil || len(first) != 1 || first[0].Action != "opened" {
		t.Fatalf("EvaluateItem (opened): events=%#v err=%v", first, err)
	}

	updated := opened
	updated.Title = "Updated title"
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Minute)
	second, err := evaluator.EvaluateItem(ctx, monitor, updated)
	if err != nil || len(second) != 0 {
		t.Fatalf("EvaluateItem (updated): events=%#v err=%v, want no duplicate", second, err)
	}

	closed := updated
	closed.State = protocol.GitHubItemClosed
	closed.UpdatedAt = closed.UpdatedAt.Add(time.Minute)
	third, err := evaluator.EvaluateItem(ctx, monitor, closed)
	if err != nil || len(third) != 0 {
		t.Fatalf("EvaluateItem (closed): events=%#v err=%v, want no duplicate", third, err)
	}
}
