-- Earlier versions allowed one automatic event per action/state change. Keep
-- the oldest event as the automatic delivery record and preserve later legacy
-- rows as history without a delivery key (the same representation used for
-- explicitly replayed events).
WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (
        PARTITION BY rule_id, kind, number
        ORDER BY created_at, id
    ) AS occurrence
    FROM github_monitor_events
    WHERE rule_id IS NOT NULL AND delivery_key IS NOT NULL
)
UPDATE github_monitor_events
SET delivery_key = NULL
WHERE id IN (SELECT id FROM ranked WHERE occurrence > 1);

CREATE UNIQUE INDEX github_monitor_events_rule_item_unique_idx
ON github_monitor_events(rule_id, kind, number)
WHERE rule_id IS NOT NULL AND delivery_key IS NOT NULL;
