-- Preserve historical and already queued runs at their original 120-second limit.
-- New workers explicitly snapshot the configured limit when accepting a run.
ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS execution_timeout_seconds INTEGER NOT NULL DEFAULT 120
    CHECK (execution_timeout_seconds BETWEEN 30 AND 1800);
