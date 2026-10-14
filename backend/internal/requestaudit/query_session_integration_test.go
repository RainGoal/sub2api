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

func testGlobalSessionFilter(t *testing.T, ctx context.Context, db *sql.DB, m *Manager) {
	t.Helper()
	session, firstID, secondID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, row := range []struct {
		id, session, cleanup string
		key, ageMinutes      int
	}{
		{firstID, session, "active", 601, 1},
		{secondID, session, "active", 602, 2},
		{uuid.NewString(), "other-" + session, "active", 601, 1},
		{uuid.NewString(), session, "active", 603, 31 * 24 * 60},
		{uuid.NewString(), session, "deleting", 604, 3},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO request_audits(id,received_at,kind,instance_id,api_key_id,session_id,cleanup_state)
VALUES($1,NOW()-($5::int*INTERVAL '1 minute'),'http_request','session-query-test',$2,$3,$4)`, row.id, row.key, row.session, row.cleanup, row.ageMinutes)
		require.NoError(t, err)
	}
	all, err := m.List(ctx, ListFilter{SessionID: session})
	require.NoError(t, err)
	require.Len(t, all.Items, 2)
	require.Equal(t, []string{firstID, secondID}, []string{all.Items[0].ID, all.Items[1].ID})
	require.Equal(t, []int64{601, 602}, []int64{all.Items[0].APIKeyID, all.Items[1].APIKeyID})
	for _, key := range []int64{601, 602} {
		filtered, err := m.List(ctx, ListFilter{SessionID: session, APIKeyID: key})
		require.NoError(t, err)
		require.Len(t, filtered.Items, 1)
		require.Equal(t, key, filtered.Items[0].APIKeyID)
	}
	first, err := m.List(ctx, ListFilter{SessionID: session, Limit: 1})
	require.NoError(t, err)
	require.True(t, first.HasMore)
	require.Len(t, first.Items, 1)
	require.Equal(t, firstID, first.Items[0].ID)
	second, err := m.List(ctx, ListFilter{SessionID: session, Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.False(t, second.HasMore)
	require.Equal(t, secondID, second.Items[0].ID)
}

func testGlobalSessionIndexPlan(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO request_audits(id,received_at,kind,instance_id,api_key_id,session_id)
SELECT md5('session-plan-'||s::text)::uuid,NOW()-s*INTERVAL '1 second','http_request','test',s%100,'plan-session-'||(s%400)::text
FROM generate_series(1,20000) s; ANALYZE request_audits`)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	// Verify that the session-only page has an ordered index path, independently
	// of fixture-dependent cost estimates for a tiny, isolated database.
	_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off`)
	require.NoError(t, err)
	rows, err := tx.QueryContext(ctx, `EXPLAIN `+recordSelect+` WHERE `+visibleRecordSQL+` AND session_id=$1 ORDER BY received_at DESC,id DESC LIMIT 51`, "plan-session-42")
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
	require.Contains(t, plan, "Index Scan using idx_request_audits_global_session_time", plan)
	require.Contains(t, plan, "session_id =", plan)
	require.NotContains(t, plan, "Sort", plan)
}
