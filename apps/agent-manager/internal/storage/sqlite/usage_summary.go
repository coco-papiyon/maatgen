package sqlite

import (
	"context"
	"fmt"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

var usageSummaryFormats = map[string]string{
	"day":   "%Y-%m-%d",
	"week":  "%Y-W%W",
	"month": "%Y-%m",
}

// GetUsageSummary aggregates usage per period, broken down into a series per
// period: by provider when no provider filter is set (so callers can compare
// providers), or by model once a provider is chosen (comparing that
// provider's models).
func (s *Store) GetUsageSummary(ctx context.Context, granularity, provider, model string) (protocol.UsageSummary, error) {
	format, ok := usageSummaryFormats[granularity]
	if !ok {
		return protocol.UsageSummary{}, fmt.Errorf("get usage summary: unsupported granularity %q", granularity)
	}

	seriesBy := "provider"
	seriesExpr := "s.agent"
	if provider != "" {
		seriesBy = "model"
		seriesExpr = "COALESCE(u.actual_model, u.model)"
	}

	query := `
		SELECT strftime(?, r.started_at) AS period,
		       ` + seriesExpr + ` AS series_key,
		       SUM(COALESCE(u.cost_usd, 0)),
		       SUM(COALESCE(u.ai_credits, 0)),
		       SUM(COALESCE(u.total_tokens, 0)),
		       SUM(COALESCE(u.input_tokens, 0)),
		       SUM(COALESCE(u.cached_input_tokens, 0)),
		       SUM(COALESCE(u.output_tokens, 0)),
		       SUM(COALESCE(u.reasoning_output_tokens, 0))
		FROM runs r
		JOIN sessions s ON s.id = r.session_id
		JOIN run_usage u ON u.run_id = r.id
		WHERE r.started_at IS NOT NULL AND ` + seriesExpr + ` IS NOT NULL`
	args := []any{format}
	if provider != "" {
		query += " AND s.agent = ?"
		args = append(args, provider)
	}
	if model != "" {
		query += " AND COALESCE(u.actual_model, u.model) = ?"
		args = append(args, model)
	}
	query += " GROUP BY period, series_key ORDER BY period ASC, series_key ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return protocol.UsageSummary{}, fmt.Errorf("get usage summary: %w", err)
	}
	defer rows.Close()

	result := protocol.UsageSummary{Granularity: granularity, SeriesBy: seriesBy, Periods: []protocol.UsagePeriod{}}
	if provider != "" {
		result.Provider = &provider
	}
	if model != "" {
		result.Model = &model
	}
	periodIndex := map[string]int{}
	for rows.Next() {
		var point protocol.UsageSeriesPoint
		var period string
		var inputTokens, cachedInputTokens, outputTokens, reasoningOutputTokens int64
		if err := rows.Scan(&period, &point.Key, &point.CostUSD, &point.AICredits, &point.TotalTokens,
			&inputTokens, &cachedInputTokens, &outputTokens, &reasoningOutputTokens); err != nil {
			return protocol.UsageSummary{}, fmt.Errorf("get usage summary: scan: %w", err)
		}
		idx, ok := periodIndex[period]
		if !ok {
			idx = len(result.Periods)
			periodIndex[period] = idx
			result.Periods = append(result.Periods, protocol.UsagePeriod{Period: period, Series: []protocol.UsageSeriesPoint{}})
		}
		entry := &result.Periods[idx]
		entry.CostUSD += point.CostUSD
		entry.AICredits += point.AICredits
		entry.TotalTokens += point.TotalTokens
		entry.InputTokens += inputTokens
		entry.CachedInputTokens += cachedInputTokens
		entry.OutputTokens += outputTokens
		entry.ReasoningOutputTokens += reasoningOutputTokens
		entry.Series = append(entry.Series, point)
	}
	if err := rows.Err(); err != nil {
		return protocol.UsageSummary{}, fmt.Errorf("get usage summary: %w", err)
	}
	return result, nil
}

func (s *Store) ListUsageProviders(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.agent
		FROM run_usage u
		JOIN runs r ON r.id = u.run_id
		JOIN sessions s ON s.id = r.session_id
		ORDER BY s.agent ASC`)
	if err != nil {
		return nil, fmt.Errorf("list usage providers: %w", err)
	}
	defer rows.Close()

	providers := []string{}
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, fmt.Errorf("list usage providers: scan: %w", err)
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list usage providers: %w", err)
	}
	return providers, nil
}

func (s *Store) ListUsageModels(ctx context.Context, provider string) ([]string, error) {
	query := `
		SELECT DISTINCT COALESCE(u.actual_model, u.model) AS model
		FROM run_usage u
		JOIN runs r ON r.id = u.run_id
		JOIN sessions s ON s.id = r.session_id
		WHERE COALESCE(u.actual_model, u.model) IS NOT NULL`
	args := []any{}
	if provider != "" {
		query += " AND s.agent = ?"
		args = append(args, provider)
	}
	query += " ORDER BY model ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list usage models: %w", err)
	}
	defer rows.Close()

	models := []string{}
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, fmt.Errorf("list usage models: scan: %w", err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list usage models: %w", err)
	}
	return models, nil
}
