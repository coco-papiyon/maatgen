package sqlite

import (
	"context"
	"fmt"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/pricing"
)

func (s *Store) UpsertModelPricing(ctx context.Context, value pricing.ModelPricing) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO model_pricing(provider, model, input_per_million, cached_input_per_million,
			cache_write_per_million, output_per_million, source_url, retrieved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, model) DO UPDATE SET
			input_per_million = excluded.input_per_million,
			cached_input_per_million = excluded.cached_input_per_million,
			cache_write_per_million = excluded.cache_write_per_million,
			output_per_million = excluded.output_per_million,
			source_url = excluded.source_url,
			retrieved_at = excluded.retrieved_at`,
		value.Provider, value.Model, value.InputPerMillion, value.CachedInputPerMillion,
		value.CacheWritePerMillion, value.OutputPerMillion, value.SourceURL, formatTime(value.RetrievedAt))
	if err != nil {
		return fmt.Errorf("upsert model pricing: %w", err)
	}
	return nil
}

func (s *Store) GetModelPricing(ctx context.Context, provider, model string) (pricing.ModelPricing, error) {
	var value pricing.ModelPricing
	var retrieved string
	err := s.db.QueryRowContext(ctx, `SELECT provider, model, input_per_million, cached_input_per_million,
		cache_write_per_million, output_per_million, source_url, retrieved_at
		FROM model_pricing WHERE provider = ? AND model = ?`, provider, model).Scan(
		&value.Provider, &value.Model, &value.InputPerMillion, &value.CachedInputPerMillion,
		&value.CacheWritePerMillion, &value.OutputPerMillion, &value.SourceURL, &retrieved)
	if err != nil {
		return pricing.ModelPricing{}, err
	}
	parsed, err := parseTime(retrieved)
	if err != nil {
		return pricing.ModelPricing{}, fmt.Errorf("parse model pricing retrieved_at: %w", err)
	}
	value.RetrievedAt = parsed
	return value, nil
}
