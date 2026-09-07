ALTER TABLE sessions ADD COLUMN workspace_kind TEXT NOT NULL DEFAULT 'git_repository'
    CHECK (workspace_kind IN ('git_repository', 'directory'));
