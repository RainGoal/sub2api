package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomSeedanceVideoTasksMigration(t *testing.T) {
	content, err := FS.ReadFile("230z_custom_seedance_video_tasks.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS custom_seedance_video_tasks")
	require.Contains(t, sql, "state_id VARCHAR(128) NOT NULL UNIQUE")
	require.Contains(t, sql, "provider_task_id VARCHAR(512)")
	require.Contains(t, sql, "hold_id VARCHAR(128) NOT NULL")
	require.Contains(t, sql, "CHECK (settlement_status IN ('pending', 'processing', 'settled', 'released'))")
	require.Contains(t, sql, "custom_seedance_video_tasks_owner_task_uq")
	require.Contains(t, sql, "WHERE provider_task_id IS NOT NULL AND provider_task_id <> ''")
	require.Contains(t, sql, "custom_seedance_video_tasks_due_idx")
	require.Contains(t, sql, "WHERE settlement_status IN ('pending', 'processing')")
}

func TestCustomSeedanceVideoProviderMigration(t *testing.T) {
	content, err := FS.ReadFile("230za_custom_seedance_video_provider.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS provider_protocol VARCHAR(32) NOT NULL DEFAULT 'bblabu_v1'")
	require.Contains(t, sql, "custom_seedance_video_tasks_provider_idx")
}
