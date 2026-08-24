ALTER TABLE github_repository_monitors
    ADD COLUMN project_name TEXT NOT NULL DEFAULT '';
