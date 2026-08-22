-- Migration: 129_seed_claude_code_template
-- 内置「Claude Code 伪装」请求模板，提供 API Key 上游需要的客户端头。
-- Anthropic 探活 adapter 会动态生成 system / metadata.user_id；模板不再保存
-- 静态 session，避免 CLI 版本与 metadata 格式不一致。
--
-- ON CONFLICT DO NOTHING：已部署环境（手动建过模板）跑此 migration 不会重复 / 覆盖。
-- 用户可自行编辑后续覆盖此 seed；CC 升大版时再起一条 migration 提供新模板，不动用户的旧模板。

INSERT INTO channel_monitor_request_templates (
    name, provider, description, extra_headers, body_override_mode, body_override
)
VALUES (
    'Claude Code 伪装',
    'anthropic',
    'Claude Code API Key 兼容请求：UA + API Key beta + X-App；请求体中的 session metadata 由每次探活动态生成。',
    '{
        "User-Agent": "claude-cli/2.1.220 (external, cli)",
        "X-Stainless-Lang": "js",
        "X-Stainless-Package-Version": "0.94.0",
        "X-Stainless-OS": "Linux",
        "X-Stainless-Arch": "arm64",
        "X-Stainless-Runtime": "node",
        "X-Stainless-Runtime-Version": "v24.3.0",
        "X-Stainless-Retry-Count": "0",
        "X-Stainless-Timeout": "600",
        "X-App": "cli",
        "anthropic-version": "2023-06-01",
        "anthropic-beta": "claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14",
        "Anthropic-Dangerous-Direct-Browser-Access": "true"
    }'::jsonb,
    'merge',
    '{
        "temperature": 1
    }'::jsonb
)
ON CONFLICT (provider, name) DO NOTHING;
