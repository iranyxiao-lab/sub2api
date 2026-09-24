SET LOCAL lock_timeout = '3s';

ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS request_key VARCHAR(100),
    ADD COLUMN IF NOT EXISTS trigger_type VARCHAR(16) NOT NULL DEFAULT 'legacy',
    ADD COLUMN IF NOT EXISTS run_token VARCHAR(64),
    ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS error_code VARCHAR(40) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS output_truncated BOOLEAN NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS idx_intelligence_run_request
    ON scheduled_test_results(plan_id, request_key) WHERE request_key IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_intelligence_run_active
    ON scheduled_test_results(plan_id) WHERE status IN ('queued', 'running');
CREATE INDEX IF NOT EXISTS idx_intelligence_run_queue
    ON scheduled_test_results(created_at, id) WHERE status IN ('queued', 'running');
