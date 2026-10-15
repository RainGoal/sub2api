//go:build integration

package requestaudit

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func testGroupFilter(t *testing.T, ctx context.Context, db *sql.DB, m *Manager) {
	t.Helper()
	firstID, secondID := uuid.NewString(), uuid.NewString()
	for _, row := range []struct {
		id, cleanup            string
		group, key, ageMinutes int
	}{
		{firstID, "active", 901, 701, 1},
		{secondID, "active", 901, 702, 2},
		{uuid.NewString(), "active", 902, 701, 1},
		{uuid.NewString(), "active", 901, 703, 31 * 24 * 60},
		{uuid.NewString(), "deleting", 901, 704, 3},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO request_audits(id,received_at,kind,instance_id,group_id,api_key_id,cleanup_state)
VALUES($1,NOW()-($5::int*INTERVAL '1 minute'),'http_request','group-query-test',$2,$3,$4)`, row.id, row.group, row.key, row.cleanup, row.ageMinutes)
		require.NoError(t, err)
	}
	all, err := m.List(ctx, ListFilter{GroupID: 901})
	require.NoError(t, err)
	require.Len(t, all.Items, 2)
	require.Equal(t, []string{firstID, secondID}, []string{all.Items[0].ID, all.Items[1].ID})
	narrowed, err := m.List(ctx, ListFilter{GroupID: 901, APIKeyID: 702})
	require.NoError(t, err)
	require.Len(t, narrowed.Items, 1)
	require.Equal(t, secondID, narrowed.Items[0].ID)
	first, err := m.List(ctx, ListFilter{GroupID: 901, Limit: 1})
	require.NoError(t, err)
	require.True(t, first.HasMore)
	require.Len(t, first.Items, 1)
	second, err := m.List(ctx, ListFilter{GroupID: 901, Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.False(t, second.HasMore)
	require.Equal(t, secondID, second.Items[0].ID)
}

func testGroupIndexPlan(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO request_audits(id,received_at,kind,instance_id,group_id)
SELECT md5('group-plan-'||s::text)::uuid,NOW()-s*INTERVAL '1 second','http_request','test',1000+s%400
FROM generate_series(1,4000) s; ANALYZE request_audits`)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off`)
	require.NoError(t, err)
	rows, err := tx.QueryContext(ctx, `EXPLAIN `+recordSelect+` WHERE `+visibleRecordSQL+` AND group_id=$1 ORDER BY received_at DESC,id DESC LIMIT 51`, 1042)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		lines = append(lines, line)
	}
	require.NoError(t, rows.Err())
	plan := strings.Join(lines, "\n")
	require.Contains(t, plan, "Index Scan using idx_request_audits_group_time", plan)
	require.Contains(t, plan, "group_id =", plan)
	require.NotContains(t, plan, "Sort", plan)
}
