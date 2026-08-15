CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    agent TEXT NOT NULL CHECK (agent IN ('codex')),
    workspace TEXT NOT NULL,
    worktree TEXT NOT NULL,
    base_commit TEXT NOT NULL,
    codex_thread_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('active', 'closed')),
    created_at TEXT NOT NULL,
    closed_at TEXT
);

CREATE INDEX IF NOT EXISTS sessions_created_at_idx
    ON sessions(created_at DESC);

CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('queued', 'starting', 'running', 'completed', 'failed', 'cancelled')),
    prompt TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    exit_code INTEGER,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS runs_session_created_at_idx
    ON runs(session_id, created_at);

CREATE TABLE IF NOT EXISTS events (
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

CREATE TABLE IF NOT EXISTS run_usage (
    run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    input_tokens INTEGER,
    cached_input_tokens INTEGER,
    output_tokens INTEGER,
    reasoning_output_tokens INTEGER,
    total_tokens INTEGER,
    source TEXT NOT NULL CHECK (source IN ('cli', 'unknown')),
    raw_json TEXT
);

CREATE TABLE IF NOT EXISTS redacted_raw_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    agent TEXT NOT NULL,
    raw_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS changes (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    old_path TEXT,
    change_kind TEXT NOT NULL,
    review_mode TEXT NOT NULL,
    old_start INTEGER,
    old_lines INTEGER,
    new_start INTEGER,
    new_lines INTEGER,
    original_text TEXT,
    modified_text TEXT,
    status TEXT NOT NULL,
    base_hash TEXT,
    expected_hash TEXT,
    reviewed_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
