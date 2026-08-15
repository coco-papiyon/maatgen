package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/pricing"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// BackfillRunCosts calculates and persists costs for historical usage rows that
// were recorded before cost calculation was enabled. Rows without an actual
// model use the currently configured provider default supplied by the caller.
func (s *Store) BackfillRunCosts(ctx context.Context, fallbackModels map[string]string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.run_id, s.agent, u.input_tokens, u.cached_input_tokens,
			u.output_tokens, u.actual_model, u.ai_credits
		FROM run_usage u
		JOIN runs r ON r.id = u.run_id
		JOIN sessions s ON s.id = r.session_id
		WHERE u.cost_usd IS NULL`)
	if err != nil { return 0, fmt.Errorf("list run costs for backfill: %w", err) }
	type candidate struct {
		runID, provider string
		model sql.NullString
		input, cached, output sql.NullInt64
		credits sql.NullFloat64
	}
	candidates := make([]candidate, 0)
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.runID, &value.provider, &value.input, &value.cached, &value.output, &value.model, &value.credits); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan run cost for backfill: %w", err)
		}
		candidates = append(candidates, value)
	}
	if err := rows.Err(); err != nil { _ = rows.Close(); return 0, fmt.Errorf("iterate run costs for backfill: %w", err) }
	if err := rows.Close(); err != nil { return 0, fmt.Errorf("close run costs for backfill: %w", err) }
	updated := 0
	for _, value := range candidates {
		model := value.model.String
		if model == "" { model = fallbackModels[value.provider] }
		usage := protocol.TokenUsage{}
		if model != "" { usage.ActualModel = &model }
		usage.InputTokens = int64Pointer(value.input)
		usage.CachedInputTokens = int64Pointer(value.cached)
		usage.OutputTokens = int64Pointer(value.output)
		usage.AICredits = float64Pointer(value.credits)
		var cost float64
		if usage.AICredits != nil {
			cost = pricing.CostUSD(usage, pricing.ModelPricing{}, usage.AICredits)
		} else {
			if model == "" { continue }
			rates, err := s.GetModelPricing(ctx, value.provider, model)
			if err != nil { continue }
			cost = pricing.CostUSD(usage, rates, nil)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE run_usage SET cost_usd = ? WHERE run_id = ? AND cost_usd IS NULL`, cost, value.runID); err != nil {
			return updated, fmt.Errorf("update run cost for backfill: %w", err)
		}
		updated++
	}
	return updated, nil
}
