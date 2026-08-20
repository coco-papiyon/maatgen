package pricing

import "time"

// staticPricing holds published API prices for providers whose CLIs report
// per-turn cost themselves (so dynamic scraping is not used). These values
// are seeded into the database at startup and shown in the model selector.
var staticPricing = []ModelPricing{
	{Provider: "claude", Model: "claude-opus-5", InputPerMillion: 15, CachedInputPerMillion: 1.5, CacheWritePerMillion: 18.75, OutputPerMillion: 75},
	{Provider: "claude", Model: "claude-sonnet-5", InputPerMillion: 3, CachedInputPerMillion: 0.3, CacheWritePerMillion: 3.75, OutputPerMillion: 15},
	{Provider: "claude", Model: "claude-sonnet-4-6", InputPerMillion: 3, CachedInputPerMillion: 0.3, CacheWritePerMillion: 3.75, OutputPerMillion: 15},
	{Provider: "claude", Model: "claude-haiku-4-5", InputPerMillion: 0.8, CachedInputPerMillion: 0.08, CacheWritePerMillion: 1, OutputPerMillion: 4},
}

// Static returns hardcoded pricing records for providers not covered by
// dynamic scraping. RetrievedAt is set to the zero time so callers can
// distinguish static entries from live-fetched ones if needed.
func Static() []ModelPricing {
	result := make([]ModelPricing, len(staticPricing))
	for i, p := range staticPricing {
		p.SourceURL = "https://www.anthropic.com/pricing"
		p.RetrievedAt = time.Time{}
		result[i] = p
	}
	return result
}
