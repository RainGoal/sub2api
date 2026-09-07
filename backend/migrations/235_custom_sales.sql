-- Sales partners share the existing users and billing system. Financial history
-- deliberately has no cascading foreign keys and uses the billing USD scale.
CREATE TABLE IF NOT EXISTS sales_partners (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id),
    name VARCHAR(100) NOT NULL,
    code VARCHAR(48) NOT NULL UNIQUE,
    hostname VARCHAR(253) NOT NULL UNIQUE,
    commission_rate NUMERIC(12,8) NOT NULL CHECK (commission_rate BETWEEN 0 AND 100),
    promotion_enabled BOOLEAN NOT NULL DEFAULT false,
    accrual_enabled BOOLEAN NOT NULL DEFAULT false,
    payout_frozen BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (code = lower(code)), CHECK (hostname = lower(hostname))
);
CREATE TABLE IF NOT EXISTS sales_commission_rules (
    id BIGSERIAL PRIMARY KEY,
    partner_id BIGINT NOT NULL REFERENCES sales_partners(id),
    commission_rate NUMERIC(12,8) NOT NULL CHECK (commission_rate BETWEEN 0 AND 100),
    effective_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_user_id BIGINT NOT NULL REFERENCES users(id)
);
CREATE INDEX IF NOT EXISTS idx_sales_rules_partner_effective ON sales_commission_rules(partner_id, effective_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS sales_customers (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    partner_id BIGINT NOT NULL REFERENCES sales_partners(id),
    referral_code VARCHAR(48) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sales_customers_partner_created ON sales_customers(partner_id, created_at, user_id);
CREATE TABLE IF NOT EXISTS sales_commission_events (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    api_key_id BIGINT NOT NULL,
    customer_user_id BIGINT NOT NULL REFERENCES users(id),
    partner_id BIGINT NOT NULL REFERENCES sales_partners(id),
    rule_id BIGINT NOT NULL REFERENCES sales_commission_rules(id),
    revenue NUMERIC(20,8) NOT NULL,
    cost NUMERIC(20,8) NOT NULL,
    profit NUMERIC(20,8) NOT NULL,
    commission_rate NUMERIC(12,8) NOT NULL,
    commission NUMERIC(20,8) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    model VARCHAR(255) NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    UNIQUE (request_id, api_key_id),
    CHECK (revenue >= 0 AND cost >= 0),
    CHECK (commission_rate BETWEEN 0 AND 100),
    CHECK (profit = revenue - cost),
    CHECK (commission = ROUND(profit * commission_rate / 100, 8))
);
CREATE INDEX IF NOT EXISTS idx_sales_events_pending ON sales_commission_events(partner_id, occurred_at, id) WHERE processed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sales_events_customer ON sales_commission_events(customer_user_id);
CREATE TABLE IF NOT EXISTS sales_settlements (
    id BIGSERIAL PRIMARY KEY,
    partner_id BIGINT NOT NULL REFERENCES sales_partners(id),
    month VARCHAR(7) NOT NULL,
    cutoff TIMESTAMPTZ NOT NULL,
    net_commission NUMERIC(20,8) NOT NULL,
    payout_amount NUMERIC(20,8) NOT NULL CHECK (payout_amount >= 0),
    carry_amount NUMERIC(20,8) NOT NULL CHECK (carry_amount <= 0),
    status VARCHAR(16) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'confirmed', 'paid')),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    request_key VARCHAR(128) NOT NULL UNIQUE,
    confirm_request_key VARCHAR(128) UNIQUE,
    pay_request_key VARCHAR(128) UNIQUE,
    payment_reference VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at TIMESTAMPTZ,
    paid_at TIMESTAMPTZ,
    actor_user_id BIGINT NOT NULL REFERENCES users(id),
    UNIQUE (partner_id, month)
);
CREATE TABLE IF NOT EXISTS sales_commission_ledger (
    id BIGSERIAL PRIMARY KEY,
    partner_id BIGINT NOT NULL REFERENCES sales_partners(id),
    customer_user_id BIGINT REFERENCES users(id),
    event_id BIGINT UNIQUE REFERENCES sales_commission_events(id),
    source_ledger_id BIGINT REFERENCES sales_commission_ledger(id),
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('usage', 'adjustment', 'carry')),
    revenue NUMERIC(20,8) NOT NULL DEFAULT 0,
    cost NUMERIC(20,8) NOT NULL DEFAULT 0,
    profit NUMERIC(20,8) NOT NULL DEFAULT 0,
    commission_rate NUMERIC(12,8) NOT NULL DEFAULT 0,
    commission NUMERIC(20,8) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    note VARCHAR(500) NOT NULL DEFAULT '',
    request_key VARCHAR(128) UNIQUE,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_user_id BIGINT REFERENCES users(id)
);
CREATE INDEX IF NOT EXISTS idx_sales_ledger_partner_occurred ON sales_commission_ledger(partner_id, occurred_at, id);
CREATE INDEX IF NOT EXISTS idx_sales_ledger_customer_partner ON sales_commission_ledger(customer_user_id, partner_id);
-- Association table keeps the ledger itself immutable when settling.
CREATE TABLE IF NOT EXISTS sales_settlement_items (
    ledger_id BIGINT PRIMARY KEY REFERENCES sales_commission_ledger(id),
    settlement_id BIGINT NOT NULL REFERENCES sales_settlements(id)
);
CREATE INDEX IF NOT EXISTS idx_sales_settlement_items_settlement ON sales_settlement_items(settlement_id);
CREATE TABLE IF NOT EXISTS sales_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT NOT NULL REFERENCES users(id),
    action VARCHAR(64) NOT NULL,
    target_id BIGINT,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO settings(key, value, updated_at)
VALUES ('custom_sales_config', '{"enabled":false,"main_frontend_url":""}', NOW())
ON CONFLICT (key) DO NOTHING;

CREATE OR REPLACE FUNCTION sales_reject_financial_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'sales financial snapshots are immutable';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_sales_ledger_immutable ON sales_commission_ledger;
CREATE TRIGGER trg_sales_ledger_immutable BEFORE UPDATE OR DELETE ON sales_commission_ledger
FOR EACH ROW EXECUTE FUNCTION sales_reject_financial_mutation();
DROP TRIGGER IF EXISTS trg_sales_rules_immutable ON sales_commission_rules;
CREATE TRIGGER trg_sales_rules_immutable BEFORE UPDATE OR DELETE ON sales_commission_rules
FOR EACH ROW EXECUTE FUNCTION sales_reject_financial_mutation();
CREATE OR REPLACE FUNCTION sales_guard_event_snapshot() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (to_jsonb(NEW) - 'processed_at') IS DISTINCT FROM (to_jsonb(OLD) - 'processed_at') THEN
        RAISE EXCEPTION 'sales commission event snapshots are immutable';
    END IF;
    IF OLD.processed_at IS NOT NULL AND NEW.processed_at IS DISTINCT FROM OLD.processed_at THEN
        RAISE EXCEPTION 'sales processed event cannot be reset';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_sales_events_immutable ON sales_commission_events;
CREATE TRIGGER trg_sales_events_immutable BEFORE UPDATE OR DELETE ON sales_commission_events
FOR EACH ROW EXECUTE FUNCTION sales_guard_event_snapshot();
