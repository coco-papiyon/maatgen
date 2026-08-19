CREATE TABLE session_source_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    language TEXT NOT NULL,
    files INTEGER NOT NULL,
    blank INTEGER NOT NULL,
    comment INTEGER NOT NULL,
    code INTEGER NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX session_source_stats_session_idx ON session_source_stats(session_id);
