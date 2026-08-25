-- Durable state for the custom bblabu Seedance video provider.
--
-- The custom suffix keeps this fork-owned schema after the current upstream
-- baseline while allowing future numbered upstream migrations to retain their
-- natural order. The table is the accounting source of truth; Redis remains an
-- optional affinity cache only.
CREATE TABLE IF NOT EXISTS custom_seedance_video_tasks (
    id BIGSERIAL PRIMARY KEY,
    state_id VARCHAR(128) NOT NULL UNIQUE,
    provider_task_id VARCHAR(512),
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    group_id BIGINT,
    account_id BIGINT,
    model VARCHAR(128) NOT NULL,
    resolution VARCHAR(32) NOT NULL,
    duration_seconds INTEGER NOT NULL CHECK (duration_seconds > 0),
    reference_video_count INTEGER NOT NULL DEFAULT 0 CHECK (reference_video_count >= 0),
    original_model VARCHAR(128) NOT NULL DEFAULT '',
    request_payload_hash VARCHAR(128) NOT NULL DEFAULT '',
    hold_id VARCHAR(128) NOT NULL,
    hold_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (hold_amount >= 0),
    total_cost_per_second DECIMAL(20,10) NOT NULL CHECK (total_cost_per_second >= 0),
    actual_cost_per_second DECIMAL(20,10) NOT NULL CHECK (actual_cost_per_second >= 0),
    rate_multiplier DECIMAL(20,10) NOT NULL CHECK (rate_multiplier >= 0),
    is_subscription_billing BOOLEAN NOT NULL DEFAULT FALSE,
    subscription_id BIGINT,
    upstream_status VARCHAR(32) NOT NULL DEFAULT 'creating',
    settlement_status VARCHAR(32) NOT NULL DEFAULT 'pending'
        CHECK (settlement_status IN ('pending', 'processing', 'settled', 'released')),
    actual_cost DECIMAL(20,8) CHECK (actual_cost IS NULL OR actual_cost >= 0),
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    last_error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS custom_seedance_video_tasks_owner_task_uq
    ON custom_seedance_video_tasks (user_id, api_key_id, provider_task_id)
    WHERE provider_task_id IS NOT NULL AND provider_task_id <> '';

CREATE INDEX IF NOT EXISTS custom_seedance_video_tasks_due_idx
    ON custom_seedance_video_tasks (settlement_status, next_poll_at)
    WHERE settlement_status IN ('pending', 'processing');

CREATE INDEX IF NOT EXISTS custom_seedance_video_tasks_account_idx
    ON custom_seedance_video_tasks (account_id, settlement_status);
