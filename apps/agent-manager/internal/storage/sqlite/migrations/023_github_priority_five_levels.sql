ALTER TABLE github_trigger_rules ADD COLUMN priority_new TEXT NOT NULL DEFAULT '3' CHECK (priority_new IN ('1', '2', '3', '4', '5'));
UPDATE github_trigger_rules SET priority_new = CASE priority WHEN 'high' THEN '1' WHEN 'medium' THEN '3' WHEN 'low' THEN '5' END;
ALTER TABLE github_trigger_rules DROP COLUMN priority;
ALTER TABLE github_trigger_rules RENAME COLUMN priority_new TO priority;
