package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestGetUsageSummary(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	createSession := func(id string, agent protocol.AgentName) {
		t.Helper()
		if err := store.CreateSession(ctx, protocol.AgentSession{
			ID:        id,
			Agent:     agent,
			Workspace: "C:/workspace/project",
			Status:    protocol.SessionActive,
			CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
	}
	createSession("session-claude", protocol.AgentClaude)
	createSession("session-codex", protocol.AgentCodex)

	seedRun := func(runID, sessionID string, startedAt time.Time, model string, costUSD, aiCredits float64, totalTokens int64, inputTokens, cachedInputTokens, outputTokens, reasoningOutputTokens int64) {
		t.Helper()
		if err := store.CreateRun(ctx, protocol.AgentRun{
			ID:        runID,
			SessionID: sessionID,
			Status:    protocol.RunCompleted,
			Prompt:    "do work",
			StartedAt: &startedAt,
		}); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
		usage := protocol.TokenUsage{
			Model:                 &model,
			CostUSD:               &costUSD,
			AICredits:             &aiCredits,
			TotalTokens:           &totalTokens,
			InputTokens:           &inputTokens,
			CachedInputTokens:     &cachedInputTokens,
			OutputTokens:          &outputTokens,
			ReasoningOutputTokens: &reasoningOutputTokens,
			Source:                "cli",
		}
		if err := store.UpsertRunUsage(ctx, runID, usage, nil); err != nil {
			t.Fatalf("upsert usage %s: %v", runID, err)
		}
	}

	seedRun("run-1", "session-claude", time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC), "claude-opus", 1.5, 0, 1000, 700, 300, 300, 0)
	seedRun("run-2", "session-claude", time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC), "claude-opus", 0.5, 0, 500, 400, 100, 100, 0)
	seedRun("run-4", "session-claude", time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC), "claude-sonnet", 1.0, 0, 200, 150, 0, 50, 0)
	seedRun("run-3", "session-codex", time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC), "gpt-5.1", 2.0, 3.25, 2000, 1200, 0, 800, 300)

	t.Run("day granularity aggregates across sessions, broken down by provider", func(t *testing.T) {
		summary, err := store.GetUsageSummary(ctx, "day", "", "")
		if err != nil {
			t.Fatalf("get usage summary: %v", err)
		}
		if summary.Granularity != "day" || summary.SeriesBy != "provider" || summary.Provider != nil || summary.Model != nil {
			t.Fatalf("summary header = %#v", summary)
		}
		if len(summary.Periods) != 2 {
			t.Fatalf("periods = %#v", summary.Periods)
		}
		if summary.Periods[0].Period != "2026-08-03" || summary.Periods[0].CostUSD != 3.0 || summary.Periods[0].TotalTokens != 1700 ||
			summary.Periods[0].InputTokens != 1250 || summary.Periods[0].CachedInputTokens != 400 ||
			summary.Periods[0].OutputTokens != 450 || summary.Periods[0].ReasoningOutputTokens != 0 {
			t.Fatalf("first period = %#v", summary.Periods[0])
		}
		if len(summary.Periods[0].Series) != 1 || summary.Periods[0].Series[0].Key != "claude" || summary.Periods[0].Series[0].CostUSD != 3.0 {
			t.Fatalf("first period series = %#v", summary.Periods[0].Series)
		}
		if summary.Periods[1].Period != "2026-08-10" || summary.Periods[1].CostUSD != 2.0 || summary.Periods[1].AICredits != 3.25 || summary.Periods[1].TotalTokens != 2000 ||
			summary.Periods[1].InputTokens != 1200 || summary.Periods[1].CachedInputTokens != 0 ||
			summary.Periods[1].OutputTokens != 800 || summary.Periods[1].ReasoningOutputTokens != 300 {
			t.Fatalf("second period = %#v", summary.Periods[1])
		}
		if len(summary.Periods[1].Series) != 1 || summary.Periods[1].Series[0].Key != "codex" || summary.Periods[1].Series[0].AICredits != 3.25 {
			t.Fatalf("second period series = %#v", summary.Periods[1].Series)
		}
	})

	t.Run("month granularity groups by month", func(t *testing.T) {
		summary, err := store.GetUsageSummary(ctx, "month", "", "")
		if err != nil {
			t.Fatalf("get usage summary: %v", err)
		}
		if len(summary.Periods) != 1 || summary.Periods[0].Period != "2026-08" || summary.Periods[0].CostUSD != 5.0 || summary.Periods[0].TotalTokens != 3700 ||
			summary.Periods[0].InputTokens != 2450 || summary.Periods[0].CachedInputTokens != 400 ||
			summary.Periods[0].OutputTokens != 1250 || summary.Periods[0].ReasoningOutputTokens != 300 {
			t.Fatalf("periods = %#v", summary.Periods)
		}
	})

	t.Run("provider filter narrows results and breaks down by model", func(t *testing.T) {
		summary, err := store.GetUsageSummary(ctx, "day", "claude", "")
		if err != nil {
			t.Fatalf("get usage summary: %v", err)
		}
		if summary.SeriesBy != "model" || summary.Provider == nil || *summary.Provider != "claude" {
			t.Fatalf("summary header = %#v", summary)
		}
		if len(summary.Periods) != 1 || summary.Periods[0].CostUSD != 3.0 || summary.Periods[0].TotalTokens != 1700 {
			t.Fatalf("periods = %#v", summary.Periods)
		}
		series := summary.Periods[0].Series
		if len(series) != 2 || series[0].Key != "claude-opus" || series[0].CostUSD != 2.0 || series[1].Key != "claude-sonnet" || series[1].CostUSD != 1.0 {
			t.Fatalf("series = %#v", series)
		}
	})

	t.Run("model filter narrows results", func(t *testing.T) {
		summary, err := store.GetUsageSummary(ctx, "day", "", "claude-opus")
		if err != nil {
			t.Fatalf("get usage summary: %v", err)
		}
		if summary.Model == nil || *summary.Model != "claude-opus" {
			t.Fatalf("summary model = %#v", summary.Model)
		}
		if len(summary.Periods) != 1 || summary.Periods[0].CostUSD != 2.0 {
			t.Fatalf("periods = %#v", summary.Periods)
		}
	})

	t.Run("provider and model filters combine", func(t *testing.T) {
		summary, err := store.GetUsageSummary(ctx, "day", "codex", "claude-opus")
		if err != nil {
			t.Fatalf("get usage summary: %v", err)
		}
		if len(summary.Periods) != 0 {
			t.Fatalf("expected no periods for mismatched provider/model, got %#v", summary.Periods)
		}
	})

	t.Run("rejects unsupported granularity", func(t *testing.T) {
		if _, err := store.GetUsageSummary(ctx, "year", "", ""); err == nil {
			t.Fatal("expected error for unsupported granularity")
		}
	})

	t.Run("list usage providers returns distinct sorted providers", func(t *testing.T) {
		providers, err := store.ListUsageProviders(ctx)
		if err != nil {
			t.Fatalf("list usage providers: %v", err)
		}
		if len(providers) != 2 || providers[0] != "claude" || providers[1] != "codex" {
			t.Fatalf("providers = %#v", providers)
		}
	})

	t.Run("list usage models returns distinct sorted models", func(t *testing.T) {
		models, err := store.ListUsageModels(ctx, "")
		if err != nil {
			t.Fatalf("list usage models: %v", err)
		}
		if len(models) != 3 || models[0] != "claude-opus" || models[1] != "claude-sonnet" || models[2] != "gpt-5.1" {
			t.Fatalf("models = %#v", models)
		}
	})

	t.Run("list usage models filters by provider", func(t *testing.T) {
		models, err := store.ListUsageModels(ctx, "codex")
		if err != nil {
			t.Fatalf("list usage models: %v", err)
		}
		if len(models) != 1 || models[0] != "gpt-5.1" {
			t.Fatalf("models = %#v", models)
		}
	})
}
