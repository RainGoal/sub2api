-- NULL preserves settlement compatibility for tasks created before this release.
-- Account assignment writes this snapshot atomically before contacting a provider.
ALTER TABLE custom_seedance_video_tasks
    ADD COLUMN IF NOT EXISTS account_cost_snapshot JSONB;
