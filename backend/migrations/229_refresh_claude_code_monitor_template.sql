-- Migration: 229_refresh_claude_code_monitor_template
-- Refresh only the original seeded Claude Code template. User-edited templates
-- are left untouched so this migration does not overwrite custom monitoring
-- configurations.

UPDATE channel_monitor_request_templates
SET description = 'Claude Code API Key 兼容请求：UA + API Key beta + X-App；请求体中的 session metadata 由每次探活动态生成。',
    extra_headers = '{
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
    body_override = '{"temperature": 1}'::jsonb
WHERE provider = 'anthropic'
  AND name = 'Claude Code 伪装'
  AND extra_headers ->> 'User-Agent' = 'claude-cli/2.1.114 (external, sdk-cli)'
  AND body_override -> 'metadata' ->> 'user_id' =
      'user_0000000000000000000000000000000000000000000000000000000000000000_account_00000000-0000-0000-0000-000000000000_session_00000000-0000-0000-0000-000000000000';

-- Template settings are copied into monitors. Refresh only unchanged snapshots
-- from the original seed; manually customized monitor snapshots are preserved.
UPDATE channel_monitors
SET extra_headers = '{
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
    body_override_mode = 'merge',
    body_override = '{"temperature": 1}'::jsonb
WHERE template_id IN (
    SELECT id
    FROM channel_monitor_request_templates
    WHERE provider = 'anthropic'
      AND name = 'Claude Code 伪装'
)
  AND extra_headers ->> 'User-Agent' = 'claude-cli/2.1.114 (external, sdk-cli)'
  AND body_override -> 'metadata' ->> 'user_id' =
      'user_0000000000000000000000000000000000000000000000000000000000000000_account_00000000-0000-0000-0000-000000000000_session_00000000-0000-0000-0000-000000000000';
