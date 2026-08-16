ALTER TABLE runs RENAME TO runs_before_command_approval;

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('queued', 'starting', 'running', 'waiting_for_approval', 'completed', 'failed', 'cancelled')),
    prompt TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    exit_code INTEGER,
    created_at TEXT NOT NULL
);

INSERT INTO runs(id, session_id, status, prompt, started_at, finished_at, exit_code, created_at)
SELECT id, session_id, status, prompt, started_at, finished_at, exit_code, created_at
FROM runs_before_command_approval;

DROP TABLE runs_before_command_approval;
CREATE INDEX runs_session_created_at_idx ON runs(session_id, created_at);

CREATE TABLE command_approvals (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    provider_request_id TEXT NOT NULL,
    command TEXT NOT NULL,
    shell TEXT NOT NULL,
    working_directory TEXT NOT NULL,
    segments_json TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'denied', 'cancelled', 'expired')),
    risk TEXT CHECK (risk IN ('safe', 'low', 'high', 'critical')),
    confidence REAL,
    summary TEXT,
    factors_json TEXT NOT NULL,
    decision TEXT CHECK (decision IN ('allow_once', 'allow_session', 'allow_permanent', 'deny')),
    scope TEXT CHECK (scope IN ('once', 'session', 'permanent')),
    source TEXT CHECK (source IN ('config', 'ai', 'human', 'system')),
    rule_argv_json TEXT,
    created_at TEXT NOT NULL,
    decided_at TEXT,
    UNIQUE(run_id, provider_request_id)
);

CREATE INDEX command_approvals_session_status_idx
    ON command_approvals(session_id, status, created_at);
