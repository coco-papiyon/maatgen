ALTER TABLE sessions ADD COLUMN cleanup_status TEXT NOT NULL DEFAULT 'not_started'
    CHECK (cleanup_status IN ('not_started', 'pending', 'completed', 'failed'));
ALTER TABLE sessions ADD COLUMN cleanup_error TEXT;
ALTER TABLE sessions ADD COLUMN cleanup_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN cleanup_updated_at TEXT;
