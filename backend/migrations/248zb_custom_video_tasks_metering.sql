-- Exact decimal quantities are retained for audit without changing historical
-- integer usage columns or reinterpreting previous task billing.
ALTER TABLE custom_seedance_video_tasks
    ADD COLUMN IF NOT EXISTS metering_snapshot JSONB;
