CREATE TABLE github_repository_monitors (
    repository TEXT PRIMARY KEY,
    host TEXT NOT NULL,
    owner TEXT NOT NULL,
    name TEXT NOT NULL,
    remote_name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    poll_interval_seconds INTEGER NOT NULL,
    coalesce_queue_limit INTEGER NOT NULL DEFAULT 20,
    last_synced_at TEXT,
    next_sync_at TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE github_trigger_rules (
    id TEXT PRIMARY KEY,
    repository TEXT NOT NULL REFERENCES github_repository_monitors(repository) ON DELETE CASCADE,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    event_kinds_json TEXT NOT NULL,
    filters_json TEXT NOT NULL,
    prompt_template TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('codex', 'claude', 'copilot')),
    model TEXT,
    reasoning_effort TEXT,
    concurrency_policy TEXT NOT NULL CHECK (concurrency_policy IN ('skip', 'coalesce')) DEFAULT 'coalesce',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX github_trigger_rules_repository_idx ON github_trigger_rules(repository);

CREATE TABLE github_observed_items (
    repository TEXT NOT NULL REFERENCES github_repository_monitors(repository) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('issue', 'pull_request')),
    number INTEGER NOT NULL,
    state_hash TEXT NOT NULL,
    last_action TEXT NOT NULL,
    projects_available INTEGER NOT NULL DEFAULT 0,
    snapshot_json TEXT NOT NULL,
    first_synced_at TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    PRIMARY KEY (repository, kind, number)
);

CREATE TABLE github_monitor_events (
    id TEXT PRIMARY KEY,
    repository TEXT NOT NULL REFERENCES github_repository_monitors(repository) ON DELETE CASCADE,
    rule_id TEXT REFERENCES github_trigger_rules(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('issue', 'pull_request')),
    number INTEGER NOT NULL,
    action TEXT NOT NULL,
    before_state_hash TEXT,
    after_state_hash TEXT NOT NULL,
    -- NULL for events created by replaying an earlier event: replays are
    -- manual re-executions and must never collide with, or consume, the
    -- original detection's dedupe key. SQLite treats each NULL in a UNIQUE
    -- column as distinct, so any number of replays may coexist.
    delivery_key TEXT,
    status TEXT NOT NULL CHECK (status IN (
        'detected', 'matched', 'queued', 'session_created', 'run_started',
        'skipped', 'completed', 'failed', 'cancelled'
    )),
    skip_reason TEXT,
    replay_of_event_id TEXT REFERENCES github_monitor_events(id) ON DELETE SET NULL,
    item_snapshot_json TEXT NOT NULL,
    session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES runs(id) ON DELETE SET NULL,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (delivery_key)
);

CREATE INDEX github_monitor_events_repository_created_idx ON github_monitor_events(repository, created_at);
CREATE INDEX github_monitor_events_status_idx ON github_monitor_events(status);
