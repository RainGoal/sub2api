-- Preserve Seedance quotas after the upstream TypeSafe platform migration.
-- The runner also preserves existing Seedance rows when executing migration 241.
-- This repair covers databases that already applied the unchanged upstream SQL.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'seedance', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe'));
