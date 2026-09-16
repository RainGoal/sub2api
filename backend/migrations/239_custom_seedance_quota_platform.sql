-- Restore Seedance for databases that already applied the upstream OpenCode
-- migration. The runner separately preserves existing Seedance rows while
-- applying the unchanged 238 migration for the first time.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'seedance', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));
