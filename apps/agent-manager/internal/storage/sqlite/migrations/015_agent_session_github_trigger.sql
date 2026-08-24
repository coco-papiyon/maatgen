-- Add GitHub monitor trigger information to sessions (ADR-007 section 4)
ALTER TABLE sessions ADD COLUMN trigger_source TEXT NOT NULL DEFAULT 'manual' CHECK(trigger_source IN ('manual', 'github_monitor'));
ALTER TABLE sessions ADD COLUMN github_monitor_event TEXT;
ALTER TABLE sessions ADD COLUMN github_rule_id TEXT;
ALTER TABLE sessions ADD COLUMN github_item_kind TEXT CHECK(github_item_kind IS NULL OR github_item_kind IN ('issue', 'pull_request'));
ALTER TABLE sessions ADD COLUMN github_item_number INTEGER;
