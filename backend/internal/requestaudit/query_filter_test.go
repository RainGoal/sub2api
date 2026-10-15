package requestaudit

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestListGroupFilterBeforeMetadataJoin(t *testing.T) {
	m, mock := mockManager(t)
	const id = "693b5cb0-cdef-4acf-a2e6-3f760debb1ee"
	query := recordSelect + ` WHERE ` + visibleRecordSQL + ` AND user_id = $1 AND api_key_id = $2 AND group_id = $3 AND session_id = $4 ORDER BY received_at DESC,id DESC LIMIT $5`
	mock.ExpectQuery(regexp.QuoteMeta(withRecordMetadata(query)+` ORDER BY r.received_at DESC,r.id DESC`)).
		WithArgs(int64(1), int64(2), int64(3), "session", 11).
		WillReturnRows(recordRows(id, "complete", "", nil))
	result, err := m.List(context.Background(), ListFilter{UserID: 1, APIKeyID: 2, GroupID: 3, SessionID: "session", Limit: 10})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, int64(3), result.Items[0].GroupID)
}

func TestListRequestIDUsesExactEntryLookupsAndCursor(t *testing.T) {
	for _, tc := range []struct{ requestID, clientID string }{
		{"client:original", "original"},
		{"client:client:original", "client:original"},
		{"raw-client", "raw-client"},
		{"resp_%literal", "resp_%literal"},
	} {
		t.Run(tc.requestID, func(t *testing.T) {
			m, mock := mockManager(t)
			const id = "693b5cb0-cdef-4acf-a2e6-3f760debb1ee"
			cursorTime := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
			query := recordSelect + ` WHERE ` + visibleRecordSQL + ` AND group_id = $1 AND kind IN ('http_request','websocket_handshake') AND id IN (SELECT id FROM request_audits WHERE ` + visibleRecordSQL + ` AND kind IN ('http_request','websocket_handshake') AND client_request_id=$2
UNION SELECT audit_id FROM request_audit_usage_links WHERE usage_request_id=$3) AND (received_at,id) < ($4,$5) ORDER BY received_at DESC,id DESC LIMIT $6`
			mock.ExpectQuery(regexp.QuoteMeta(withRecordMetadata(query)+` ORDER BY r.received_at DESC,r.id DESC`)).
				WithArgs(int64(3), tc.clientID, tc.requestID, cursorTime, id, 11).
				WillReturnRows(recordRows(id, "complete", "", nil))
			result, err := m.List(context.Background(), ListFilter{GroupID: 3, RequestID: tc.requestID, Cursor: encodeCursor(cursorTime, id), Limit: 10})
			require.NoError(t, err)
			require.Len(t, result.Items, 1)
		})
	}
}

func TestListRequestIDRejectsInvalidTextBeforeQuery(t *testing.T) {
	m, _ := mockManager(t)
	_, err := m.List(context.Background(), ListFilter{RequestID: "bad\x00id"})
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "INVALID_ARGUMENT", auditErr.Code)
}
