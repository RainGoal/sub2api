-- Independent opt-in model-plaza monitoring. Configuration stays in settings;
-- leases and results are durable so multiple application instances do not probe
-- the same target at the same time. Credentials reference existing keys.
CREATE TABLE IF NOT EXISTS model_plaza_monitor_targets (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    platform VARCHAR(40) NOT NULL,
    model VARCHAR(256) NOT NULL,
    api_mode VARCHAR(32) NOT NULL,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    signature VARCHAR(64) NOT NULL,
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds BETWEEN 1 AND 86400),
    status VARCHAR(16) NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'operational', 'degraded', 'unavailable')),
    failures INTEGER NOT NULL DEFAULT 0,
    last_checked_at TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    lease_token VARCHAR(36),
    UNIQUE (group_id, platform, model)
);
CREATE INDEX IF NOT EXISTS idx_model_plaza_monitor_targets_due ON model_plaza_monitor_targets(next_check_at);

CREATE TABLE IF NOT EXISTS model_plaza_monitor_checks (
    id BIGSERIAL PRIMARY KEY,
    target_id BIGINT NOT NULL REFERENCES model_plaza_monitor_targets(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL CHECK (status IN ('unknown', 'operational', 'degraded', 'unavailable')),
    checked_at TIMESTAMPTZ NOT NULL,
    latency_ms BIGINT,
    reason VARCHAR(48) NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_model_plaza_monitor_checks_target_time ON model_plaza_monitor_checks(target_id, checked_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_plaza_monitor_checks_time ON model_plaza_monitor_checks(checked_at);
