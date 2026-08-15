ALTER TABLE run_usage ADD COLUMN cost_usd REAL;

CREATE TABLE IF NOT EXISTS model_pricing (
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  input_per_million REAL NOT NULL,
  cached_input_per_million REAL NOT NULL,
  cache_write_per_million REAL NOT NULL,
  output_per_million REAL NOT NULL,
  source_url TEXT NOT NULL,
  retrieved_at TEXT NOT NULL,
  PRIMARY KEY (provider, model)
);
