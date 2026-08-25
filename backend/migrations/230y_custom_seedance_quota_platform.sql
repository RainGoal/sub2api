-- Add the custom Seedance provider to user x platform quota enforcement.
-- This migration is isolated from upstream migrations to reduce merge conflicts.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'seedance', 'kimi', 'zhipu', 'deepseek'));
