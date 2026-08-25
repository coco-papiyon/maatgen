ALTER TABLE github_trigger_rules ADD COLUMN priority TEXT NOT NULL CHECK (priority IN ('high', 'medium', 'low')) DEFAULT 'medium';
