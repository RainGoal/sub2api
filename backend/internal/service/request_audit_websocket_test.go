package service

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func newRequestAuditWSPolicyContext(t *testing.T) (context.Context, *requestaudit.Manager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(requestaudit.ConfigKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":false,"retention_days":3,"session_ban_enabled":true,"version":1}`))
	manager := requestaudit.NewManager(db)
	require.NoError(t, manager.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, manager.Stop(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	ingress := requestaudit.BeginIngress(manager, requestaudit.RequestMeta{})
	ingress.BindIdentity(21, 12, 1)
	ingress.SetSession("ws-session", "header.session-id", "resolved")
	return requestaudit.WithIngress(context.Background(), ingress), manager, mock
}

func TestRequestAuditWebSocketChecksNextTurnWhileCaptureDisabled(t *testing.T) {
	ctx, manager, mock := newRequestAuditWSPolicyContext(t)
	mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "ws-session").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "ws-session").WillReturnRows(sqlmock.NewRows([]string{
		"id", "api_key_id", "session_id", "public_reason", "internal_note", "source_audit_id", "session_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at",
	}).AddRow("ban-id", int64(12), "ws-session", "Public policy reason", "private note", nil, "header.session-id", int64(1), time.Now(), nil, nil, nil))
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			_, payload, err := ReadOpenAIWSClientMessage(ctx, conn, time.Second, coderws.StatusPolicyViolation, "test timeout")
			if err != nil {
				result <- err
				return
			}
			if err := conn.Write(ctx, coderws.MessageText, payload); err != nil {
				result <- err
				return
			}
		}
	}))
	defer server.Close()
	clientCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(clientCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	for _, payload := range []string{`{"type":"response.create","input":"first"}`, `{"type":"response.cancel"}`} {
		require.NoError(t, client.Write(clientCtx, coderws.MessageText, []byte(payload)))
		_, echo, err := client.Read(clientCtx)
		require.NoError(t, err)
		require.Equal(t, payload, string(echo))
	}
	require.NoError(t, client.Write(clientCtx, coderws.MessageBinary, []byte(`{"type":"response.create","event_id":"turn-two"}`)))
	_, payload, err := client.Read(clientCtx)
	require.NoError(t, err)
	require.Contains(t, string(payload), "session_banned")
	require.Contains(t, string(payload), "Public policy reason")
	require.Contains(t, string(payload), "turn-two")
	require.NotContains(t, string(payload), "private note")
	_, _, err = client.Read(clientCtx)
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
	select {
	case serverErr := <-result:
		require.ErrorIs(t, serverErr, errRequestAuditSessionBlocked)
	case <-clientCtx.Done():
		t.Fatal("server did not stop the blocked turn")
	}
	require.Zero(t, manager.Status().AcceptedRequests)
}

func TestRequestAuditWebSocketLookupFailureDoesNotRejectAudio(t *testing.T) {
	ctx, manager, mock := newRequestAuditWSPolicyContext(t)
	mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "ws-session").WillReturnError(errors.New("database unavailable"))
	require.NoError(t, auditGatewayClientWSMessage(ctx, nil, coderws.MessageText, []byte(`{"type":"input_audio_buffer.append","audio":"AA=="}`)))
	require.EqualValues(t, 1, manager.Status().BanCheckFailures)
}
