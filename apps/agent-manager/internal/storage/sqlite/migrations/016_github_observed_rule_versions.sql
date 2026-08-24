ALTER TABLE github_observed_items
ADD COLUMN evaluated_rule_versions_json TEXT NOT NULL DEFAULT '{}';
