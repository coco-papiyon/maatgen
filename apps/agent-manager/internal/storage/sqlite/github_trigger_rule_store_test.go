package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func testTriggerRule(id, repository string, createdAt time.Time) protocol.GitHubTriggerRule {
	statusValue := "Ready"
	return protocol.GitHubTriggerRule{
		ID:             id,
		Repository:     repository,
		Name:           "Design when Ready",
		Enabled:        true,
		EventKinds:     []protocol.GitHubItemKind{protocol.GitHubItemIssue},
		PromptTemplate: "Design {{.Title}}",
		Provider:       protocol.AgentCodex,
		Filters: protocol.GitHubMonitorFilters{
			Labels: []string{"bug"},
			Project: &protocol.GitHubProjectFilterCondition{
				ProjectTitle: "Roadmap",
				FieldName:    "Status",
				Value:        statusValue,
			},
		},
		ConcurrencyPolicy: protocol.GitHubConcurrencyCoalesce,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
}

func TestTriggerRuleCreateGetList(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	rule := testTriggerRule("rule-1", monitor.Repository, createdAt)
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if err := store.CreateTriggerRule(ctx, rule); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate id err = %v, want ErrConflict", err)
	}

	got, err := store.GetTriggerRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != rule.Name || got.Provider != protocol.AgentCodex || !got.Enabled {
		t.Fatalf("got = %#v", got)
	}
	if got.IncludeBody {
		t.Fatalf("includeBody = true, want false by default")
	}
	if len(got.EventKinds) != 1 || got.EventKinds[0] != protocol.GitHubItemIssue {
		t.Fatalf("eventKinds = %#v", got.EventKinds)
	}
	if len(got.Filters.Labels) != 1 || got.Filters.Labels[0] != "bug" {
		t.Fatalf("filters.Labels = %#v", got.Filters.Labels)
	}
	if got.Filters.Project == nil || got.Filters.Project.Value != "Ready" {
		t.Fatalf("filters.Project = %#v", got.Filters.Project)
	}
	if got.ConcurrencyPolicy != protocol.GitHubConcurrencyCoalesce {
		t.Fatalf("concurrencyPolicy = %v", got.ConcurrencyPolicy)
	}

	second := testTriggerRule("rule-2", monitor.Repository, createdAt.Add(time.Minute))
	if err := store.CreateTriggerRule(ctx, second); err != nil {
		t.Fatalf("create second rule: %v", err)
	}
	rules, err := store.ListTriggerRules(ctx, monitor.Repository)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rules) != 2 || rules[0].ID != "rule-1" || rules[1].ID != "rule-2" {
		t.Fatalf("rules = %#v, want creation order", rules)
	}
}

func TestTriggerRuleListAllAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitorA := testMonitor("C:/workspace/a", createdAt)
	monitorB := testMonitor("C:/workspace/b", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitorA); err != nil {
		t.Fatalf("create monitor a: %v", err)
	}
	if err := store.CreateRepositoryMonitor(ctx, monitorB); err != nil {
		t.Fatalf("create monitor b: %v", err)
	}
	if err := store.CreateTriggerRule(ctx, testTriggerRule("rule-a", monitorA.Repository, createdAt)); err != nil {
		t.Fatalf("create rule a: %v", err)
	}
	if err := store.CreateTriggerRule(ctx, testTriggerRule("rule-b", monitorB.Repository, createdAt.Add(time.Minute))); err != nil {
		t.Fatalf("create rule b: %v", err)
	}

	rules, err := store.ListAllTriggerRules(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %#v, want both repositories' rules", rules)
	}
}

func TestTriggerRuleRequiresExistingRepository(t *testing.T) {
	rule := testTriggerRule("rule-1", "missing-repository", time.Now().UTC())
	if err := newTestStore(t).CreateTriggerRule(context.Background(), rule); err == nil {
		t.Fatalf("expected a foreign key error when the repository monitor does not exist")
	}
}

func TestTriggerRuleUpdate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	rule := testTriggerRule("rule-1", monitor.Repository, createdAt)
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	rule.Name = "Renamed"
	rule.Enabled = false
	rule.EventKinds = []protocol.GitHubItemKind{protocol.GitHubItemIssue, protocol.GitHubItemPullRequest}
	rule.ConcurrencyPolicy = protocol.GitHubConcurrencySkip
	rule.IncludeBody = true
	model := "gpt-5.4-mini"
	rule.Model = &model
	updatedAt := createdAt.Add(time.Hour)
	rule.UpdatedAt = updatedAt
	if err := store.UpdateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := store.GetTriggerRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Renamed" || got.Enabled || got.ConcurrencyPolicy != protocol.GitHubConcurrencySkip {
		t.Fatalf("got = %#v", got)
	}
	if !got.IncludeBody {
		t.Fatalf("includeBody = false, want true")
	}
	if len(got.EventKinds) != 2 {
		t.Fatalf("eventKinds = %#v", got.EventKinds)
	}
	if got.Model == nil || *got.Model != model {
		t.Fatalf("model = %#v", got.Model)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updatedAt = %v, want %v", got.UpdatedAt, updatedAt)
	}
}

func TestTriggerRuleUpdateMissingReturnsNotFound(t *testing.T) {
	rule := testTriggerRule("missing", "irrelevant", time.Now().UTC())
	if err := newTestStore(t).UpdateTriggerRule(context.Background(), rule); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTriggerRuleDeleteKeepsMonitorEventHistory(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	monitor := testMonitor("C:/workspace/example", createdAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	rule := testTriggerRule("rule-1", monitor.Repository, createdAt)
	if err := store.CreateTriggerRule(ctx, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	event := testMonitorEvent("event-1", monitor.Repository, createdAt)
	event.RuleID = &rule.ID
	if inserted, err := store.InsertMonitorEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("insert event: inserted=%v err=%v", inserted, err)
	}

	if err := store.DeleteTriggerRule(ctx, rule.ID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if _, err := store.GetTriggerRule(ctx, rule.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("get deleted rule err = %v, want ErrNotFound", err)
	}
	got, err := store.GetMonitorEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("event should survive rule deletion: %v", err)
	}
	if got.RuleID != nil {
		t.Fatalf("ruleId = %#v, want nil after rule deletion", got.RuleID)
	}
}

func TestTriggerRuleDeleteMissingReturnsNotFound(t *testing.T) {
	if err := newTestStore(t).DeleteTriggerRule(context.Background(), "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
