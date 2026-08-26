ALTER TABLE github_monitor_events RENAME TO github_monitor_events_before_closed;

CREATE TABLE github_monitor_events (
    id TEXT PRIMARY KEY,
    repository TEXT NOT NULL REFERENCES github_repository_monitors(repository) ON DELETE CASCADE,
    rule_id TEXT REFERENCES github_trigger_rules(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('issue', 'pull_request')),
    number INTEGER NOT NULL,
    action TEXT NOT NULL,
    before_state_hash TEXT,
    after_state_hash TEXT NOT NULL,
    delivery_key TEXT,
    status TEXT NOT NULL CHECK (status IN (
        'detected', 'matched', 'queued', 'session_created', 'run_started',
        'skipped', 'completed', 'failed', 'cancelled', 'closed'
    )),
    skip_reason TEXT,
    replay_of_event_id TEXT REFERENCES github_monitor_events(id) ON DELETE SET NULL,
    item_snapshot_json TEXT NOT NULL,
    session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES runs(id) ON DELETE SET NULL,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at TEXT,
    UNIQUE (delivery_key)
);

INSERT INTO github_monitor_events(
    id, repository, rule_id, kind, number, action, before_state_hash, after_state_hash,
    delivery_key, status, skip_reason, replay_of_event_id, item_snapshot_json,
    session_id, run_id, last_error, created_at, updated_at, closed_at
)
SELECT
    id, repository, rule_id, kind, number, action, before_state_hash, after_state_hash,
    delivery_key, status, skip_reason, replay_of_event_id, item_snapshot_json,
    session_id, run_id, last_error, created_at, updated_at, NULL
FROM github_monitor_events_before_closed;

DROP TABLE github_monitor_events_before_closed;

CREATE INDEX github_monitor_events_repository_created_idx ON github_monitor_events(repository, created_at);
CREATE INDEX github_monitor_events_status_idx ON github_monitor_events(status);
CREATE UNIQUE INDEX github_monitor_events_rule_item_unique_idx
ON github_monitor_events(rule_id, kind, number)
WHERE rule_id IS NOT NULL AND delivery_key IS NOT NULL;
