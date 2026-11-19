package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountGroupPriorityBackfillMigration(t *testing.T) {
	content, err := FS.ReadFile("254_account_group_priority_backfill.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "WITH updated AS ( UPDATE account_groups AS account_group")
	require.Contains(t, sql, "SET priority = account.priority")
	require.Contains(t, sql, "FROM accounts AS account")
	require.Contains(t, sql, "WHERE account.id = account_group.account_id")
	require.Contains(t, sql, "account_group.priority IS DISTINCT FROM account.priority")
	require.Contains(t, sql, "RETURNING account_group.account_id, account_group.group_id")
	require.Contains(t, sql, "INSERT INTO scheduler_outbox (event_type, account_id, payload)")
	require.Contains(t, sql, "SELECT 'account_groups_changed', account_id, payload")
}
