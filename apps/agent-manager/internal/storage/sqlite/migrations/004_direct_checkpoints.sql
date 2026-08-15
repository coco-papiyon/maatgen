DROP TABLE IF EXISTS changes;
DROP TABLE IF EXISTS redacted_raw_events;
DROP TABLE IF EXISTS run_usage;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS checkpoints;
DROP TABLE IF EXISTS runs;
DROP TABLE IF EXISTS sessions;

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    agent TEXT NOT NULL CHECK (agent IN ('codex')),
    workspace TEXT NOT NULL,
    codex_thread_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('active', 'closed')),
    created_at TEXT NOT NULL,
    closed_at TEXT
);

CREATE INDEX sessions_created_at_idx ON sessions(created_at DESC);

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('queued', 'starting', 'running', 'completed', 'failed', 'cancelled')),
    prompt TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    exit_code INTEGER,
    created_at TEXT NOT NULL
);

CREATE INDEX runs_session_created_at_idx ON runs(session_id, created_at);

CREATE TABLE checkpoints (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL UNIQUE REFERENCES runs(id) ON DELETE CASCADE,
    head_commit TEXT NOT NULL,
    index_tree TEXT NOT NULL,
    before_tree TEXT NOT NULL,
    after_tree TEXT,
    before_ref TEXT NOT NULL,
    after_ref TEXT,
    created_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE INDEX checkpoints_session_created_at_idx ON checkpoints(session_id, created_at DESC);

CREATE TABLE events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('user', 'codex', 'manager')),
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(session_id, sequence)
);

CREATE TABLE run_usage (
    run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    input_tokens INTEGER,
    cached_input_tokens INTEGER,
    output_tokens INTEGER,
    reasoning_output_tokens INTEGER,
    total_tokens INTEGER,
    source TEXT NOT NULL CHECK (source IN ('cli', 'unknown')),
    raw_json TEXT
);

CREATE TABLE redacted_raw_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    agent TEXT NOT NULL,
    raw_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE changes (
    row_id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    checkpoint_id TEXT NOT NULL REFERENCES checkpoints(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL,
    hunk_id TEXT,
    old_path TEXT,
    new_path TEXT,
    change_kind TEXT NOT NULL,
    restore_mode TEXT NOT NULL,
    old_start INTEGER,
    old_lines INTEGER,
    new_start INTEGER,
    new_lines INTEGER,
    original_text TEXT,
    modified_text TEXT,
    original_file TEXT,
    modified_file TEXT,
    file_status TEXT NOT NULL,
    hunk_status TEXT,
    file_order INTEGER NOT NULL,
    hunk_order INTEGER NOT NULL,
    restored_at TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX changes_checkpoint_order_idx ON changes(checkpoint_id, file_order, hunk_order);
CREATE UNIQUE INDEX changes_checkpoint_hunk_idx
    ON changes(checkpoint_id, hunk_id) WHERE hunk_id IS NOT NULL;
