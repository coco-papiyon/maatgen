ALTER TABLE changes ADD COLUMN file_id TEXT;
ALTER TABLE changes ADD COLUMN hunk_id TEXT;
ALTER TABLE changes ADD COLUMN new_path TEXT;
ALTER TABLE changes ADD COLUMN original_file TEXT;
ALTER TABLE changes ADD COLUMN modified_file TEXT;
ALTER TABLE changes ADD COLUMN file_status TEXT;
ALTER TABLE changes ADD COLUMN hunk_status TEXT;
ALTER TABLE changes ADD COLUMN file_order INTEGER;
ALTER TABLE changes ADD COLUMN hunk_order INTEGER;

CREATE INDEX IF NOT EXISTS changes_session_order_idx
    ON changes(session_id, file_order, hunk_order);

CREATE UNIQUE INDEX IF NOT EXISTS changes_session_hunk_idx
    ON changes(session_id, hunk_id)
    WHERE hunk_id IS NOT NULL;
