package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomSeedanceQuotaPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("230y_custom_seedance_quota_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'seedance', 'kimi', 'zhipu', 'deepseek'))")
}
