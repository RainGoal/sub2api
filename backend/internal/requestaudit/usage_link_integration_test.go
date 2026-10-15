//go:build integration

package requestaudit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func testUsageRequestFilter(t *testing.T, ctx context.Context, db *sql.DB, m *Manager) {
	t.Helper()
	clientID, requestID := "client-"+uuid.NewString(), "resp_"+uuid.NewString()
	httpID, wsID, frameID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	expiredID, deletingID, otherID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, row := range []struct {
		id, kind, cleanup, client string
		key, ageMinutes           int
	}{
		{httpID, "http_request", "active", clientID, 801, 1},
		{wsID, "websocket_handshake", "active", "ws-" + clientID, 802, 2},
		{frameID, "websocket_message", "active", "ws-" + clientID, 802, 2},
		{expiredID, "http_request", "active", clientID, 801, 31 * 24 * 60},
		{deletingID, "http_request", "deleting", clientID, 801, 3},
		{otherID, "http_request", "active", "other-" + clientID, 803, 4},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO request_audits(id,received_at,kind,instance_id,api_key_id,client_request_id,cleanup_state,group_id)
VALUES($1,NOW()-($6::int*INTERVAL '1 minute'),$2,'usage-link-test',$3,$4,$5,911)`, row.id, row.kind, row.key, row.client, row.cleanup, row.ageMinutes)
		require.NoError(t, err)
	}
	for _, queryID := range []string{clientID, "client:" + clientID} {
		result, err := m.List(ctx, ListFilter{RequestID: queryID})
		require.NoError(t, err)
		require.Len(t, result.Items, 1)
		require.Equal(t, httpID, result.Items[0].ID)
	}
	link := func(id, usageID string, key int64) {
		t.Helper()
		require.NoError(t, m.persistBatch(ctx, []auditEvent{{kind: "usage_link", id: id, usageRequestID: usageID, usageAPIKeyID: key}}))
	}
	link(wsID, requestID, 802)
	link(wsID, requestID, 802)
	link(wsID, requestID+"-next", 802)
	link(otherID, requestID, 801) // A usage key cannot bind another key's entry.
	link(expiredID, requestID, 801)
	link(deletingID, requestID, 801)
	link(uuid.NewString(), requestID, 801)
	var links int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_audit_usage_links WHERE usage_request_id=$1`, requestID).Scan(&links))
	require.Equal(t, 1, links)
	for _, queryID := range []string{requestID, requestID + "-next", "ws-" + clientID, "client:ws-" + clientID} {
		result, err := m.List(ctx, ListFilter{RequestID: queryID, GroupID: 911})
		require.NoError(t, err)
		require.Len(t, result.Items, 1)
		require.Equal(t, wsID, result.Items[0].ID, "WS usage search resolves the captured connection entry")
	}
	for _, filter := range []ListFilter{{RequestID: requestID, APIKeyID: 801}, {RequestID: requestID, GroupID: 912}, {RequestID: requestID, Kind: "websocket_message"}, {RequestID: "resp_%"}} {
		result, err := m.List(ctx, filter)
		require.NoError(t, err)
		require.Empty(t, result.Items)
	}
	link(httpID, "client:"+clientID, 801)
	duplicate, err := m.List(ctx, ListFilter{RequestID: "client:" + clientID})
	require.NoError(t, err)
	require.Len(t, duplicate.Items, 1, "legacy and explicit lookup must not duplicate an entry")
	for _, update := range []string{`received_at=NOW()-INTERVAL '31 days'`, `cleanup_state='deleting'`} {
		_, err := db.ExecContext(ctx, `UPDATE request_audits SET `+update+` WHERE id=$1`, wsID)
		require.NoError(t, err)
		hidden, err := m.List(ctx, ListFilter{RequestID: requestID})
		require.NoError(t, err)
		require.Empty(t, hidden.Items, "existing links cannot expose expired or deleting entries")
		_, err = db.ExecContext(ctx, `UPDATE request_audits SET received_at=NOW()-INTERVAL '2 minutes',cleanup_state='active' WHERE id=$1`, wsID)
		require.NoError(t, err)
	}
	link(otherID, requestID, 803)
	first, err := m.List(ctx, ListFilter{RequestID: requestID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.Equal(t, wsID, first.Items[0].ID)
	require.True(t, first.HasMore)
	second, err := m.List(ctx, ListFilter{RequestID: requestID, Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.Equal(t, otherID, second.Items[0].ID)
	require.False(t, second.HasMore)
	narrowed, err := m.List(ctx, ListFilter{RequestID: requestID, APIKeyID: 803})
	require.NoError(t, err)
	require.Len(t, narrowed.Items, 1)
	require.Equal(t, otherID, narrowed.Items[0].ID)
	_, err = db.ExecContext(ctx, `DELETE FROM request_audits WHERE id=$1`, wsID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_audit_usage_links WHERE audit_id=$1`, wsID).Scan(&links))
	require.Zero(t, links, "entry cleanup must cascade to its usage links")

	// Usage work can finish after the HTTP ingress has already flushed and
	// released its request snapshot. Only its copied entry token is needed.
	ingress := BeginIngress(m, RequestMeta{Kind: "http_request"})
	ingress.BindIdentity(1, 804, 911)
	usageContext := WithUsageLinkFromContext(context.Background(), WithIngress(ctx, ingress))
	ingress.Finish(200, time.Now(), "completed", nil)
	events := takeEvents(m)
	require.NoError(t, m.persistBatch(ctx, events))
	m.releaseEvents(events)
	lateRequestID := "grok_audio:" + uuid.NewString()
	LinkUsageRequest(usageContext, lateRequestID, 804)
	events = takeEvents(m)
	require.Len(t, events, 1)
	require.NoError(t, m.persistBatch(ctx, events))
	m.releaseEvents(events)
	late, err := m.List(ctx, ListFilter{RequestID: lateRequestID})
	require.NoError(t, err)
	require.Len(t, late.Items, 1)
	require.Equal(t, ingress.capture.id, late.Items[0].ID)
}

func testRequestIDIndexPlans(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO request_audits(id,received_at,kind,instance_id,api_key_id,client_request_id)
SELECT md5('request-plan-'||s::text)::uuid,NOW()-s*INTERVAL '1 second','http_request','request-plan',999,'plan-client-'||s::text
FROM generate_series(1,1000) s;
INSERT INTO request_audit_usage_links(usage_request_id,api_key_id,audit_id)
SELECT 'plan-usage-'||s::text,999,md5('request-plan-'||s::text)::uuid FROM generate_series(1,1000) s;
ANALYZE request_audits; ANALYZE request_audit_usage_links`)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off`)
	require.NoError(t, err)
	for _, query := range []struct{ sql, value, index string }{
		{`SELECT id FROM request_audits WHERE ` + visibleRecordSQL + ` AND kind IN ('http_request','websocket_handshake') AND client_request_id=$1`, "plan-client-42", "idx_request_audits_client_request_time"},
		{`SELECT audit_id FROM request_audit_usage_links WHERE usage_request_id=$1`, "plan-usage-42", "request_audit_usage_links_pkey"},
	} {
		rows, err := tx.QueryContext(ctx, `EXPLAIN `+query.sql, query.value)
		require.NoError(t, err)
		var lines []string
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			lines = append(lines, line)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		plan := strings.Join(lines, "\n")
		require.Contains(t, plan, query.index, plan)
		require.NotContains(t, plan, "Seq Scan", plan)
	}
}
