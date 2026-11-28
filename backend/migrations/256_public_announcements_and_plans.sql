ALTER TABLE announcements
    ADD COLUMN IF NOT EXISTS is_public boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_announcements_public_active
    ON announcements (is_public, status, created_at DESC, id DESC)
    WHERE is_public = true AND status = 'active';

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS is_public boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_subscription_plans_public_for_sale
    ON subscription_plans (is_public, for_sale, sort_order ASC, id ASC)
    WHERE is_public = true AND for_sale = true;
