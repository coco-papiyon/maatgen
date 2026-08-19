ALTER TABLE sessions RENAME TO sessions_before_claude;

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    agent TEXT NOT NULL CHECK (agent IN ('codex', 'claude', 'copilot')),
    workspace TEXT NOT NULL,
    agent_thread_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('active', 'closed')),
    created_at TEXT NOT NULL,
    closed_at TEXT
);

INSERT INTO sessions(id, agent, workspace, agent_thread_id, status, created_at, closed_at)
SELECT id, agent, workspace, agent_thread_id, status, created_at, closed_at
FROM sessions_before_claude;

DROP TABLE sessions_before_claude;
CREATE INDEX sessions_created_at_idx ON sessions(created_at DESC);

ALTER TABLE events RENAME TO events_before_claude;

CREATE TABLE events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('user', 'codex', 'claude', 'copilot', 'manager')),
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(session_id, sequence)
);

INSERT INTO events(id, session_id, run_id, sequence, source, event_type, schema_version, payload_json, created_at)
SELECT id, session_id, run_id, sequence, source, event_type, schema_version, payload_json, created_at
FROM events_before_claude;

DROP TABLE events_before_claude;
