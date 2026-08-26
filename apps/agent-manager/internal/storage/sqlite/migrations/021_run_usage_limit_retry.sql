-- Track automatic retries after a Run stops on a provider usage/session
-- limit (ADR-008, issue #27). usage_limit_retry_pending_at marks a failed
-- Run as awaiting one automatic retry once usage recovers; auto_retry_of_run_id
-- links the Run started to resume it back to the original, bounding retries
-- to one per stop.
ALTER TABLE runs ADD COLUMN usage_limit_retry_pending_at TEXT;
ALTER TABLE runs ADD COLUMN auto_retry_of_run_id TEXT;
