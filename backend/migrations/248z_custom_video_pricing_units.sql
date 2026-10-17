-- Fork-owned additive video pricing metadata. NULL retains every historical unit.
ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS video_pricing_config JSONB;

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS video_pricing_config JSONB;
