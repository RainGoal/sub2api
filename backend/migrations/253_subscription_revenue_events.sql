-- Preserve the paid subscription order's USD sales basis and the exact
-- entitlement period granted by fulfillment. Older orders are intentionally
-- not backfilled because merged subscription terms cannot recover each term.
CREATE TABLE IF NOT EXISTS subscription_revenue_events (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL UNIQUE,
    subscription_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    plan_id BIGINT,
    service_start_at TIMESTAMPTZ NOT NULL,
    service_end_at TIMESTAMPTZ NOT NULL,
    order_amount NUMERIC(20, 8) NOT NULL,
    paid_amount NUMERIC(20, 8) NOT NULL,
    payment_currency VARCHAR(3) NOT NULL DEFAULT '',
    payment_fx_rate_to_usd NUMERIC(20, 12),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_subscription_revenue_events_period
    ON subscription_revenue_events (service_start_at, service_end_at);
CREATE INDEX IF NOT EXISTS idx_subscription_revenue_events_subscription
    ON subscription_revenue_events (subscription_id, service_start_at);
