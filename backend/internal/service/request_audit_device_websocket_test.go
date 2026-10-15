package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newRequestAuditDeviceWSContext(t *testing.T) (context.Context, *requestaudit.Ingress, sqlmock.Sqlmock) {
	cfg := requestaudit.DefaultConfig()
	cfg.DeviceBanEnabled = true
	return newRequestAuditDeviceWSContextWithConfig(t, cfg)
}

func newRequestAuditDeviceWSContextWithConfig(t *testing.T, cfg requestaudit.Config) (context.Context, *requestaudit.Ingress, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	value, err := json.Marshal(cfg)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(requestaudit.ConfigKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(value)))
	manager := requestaudit.NewManager(db)
	require.NoError(t, manager.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, manager.Stop(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	ingress := requestaudit.BeginIngress(manager, requestaudit.RequestMeta{})
	ingress.BindIdentity(21, 12, 1)
	return requestaudit.WithIngress(context.Background(), ingress), ingress, mock
}

func requestAuditWSDeviceBanRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "device_type", "device_id", "public_reason", "internal_note", "source_audit_id", "device_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at"}).
		AddRow("device-ban", "codex", "Original-Device", "Public device reason", "private device note", nil, "inbound", int64(1), time.Now(), nil, nil, nil)
}

func TestRequestAuditDeviceWebSocketFirstFrame(t *testing.T) {
	for _, tc := range []struct{ name, header, body string }{
		{"header", `{"installation_id":"Original-Device"}`, `{"type":"response.create"}`},
		{"body", "", `{"type":"response.create","client_metadata":{"x-codex-installation-id":"Original-Device"}}`},
		{"envelope", "", `{"type":"response.create","response":{"client_metadata":{"x-codex-turn-metadata":"{\"installation_id\":\"Original-Device\"}"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, ingress, mock := newRequestAuditDeviceWSContext(t)
			mock.ExpectQuery(`FROM device_bans`).WithArgs("codex", "Original-Device").WillReturnRows(requestAuditWSDeviceBanRows())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ctx)
			c.Request.Header.Set("X-Codex-Turn-Metadata", tc.header)
			err := AuditOpenAIWSClientFirstMessage(c, nil, coderws.MessageText, []byte(tc.body))
			require.ErrorIs(t, err, errRequestAuditDeviceBlocked)
			require.Equal(t, "Original-Device", ingress.Identity().DeviceID)
			require.Empty(t, ingress.Identity().SessionID)
		})
	}
}

func TestRequestAuditDeviceWebSocketChecksNextTurnAndKeepsOriginal(t *testing.T) {
	ctx, ingress, mock := newRequestAuditDeviceWSContext(t)
	mock.ExpectQuery(`FROM device_bans`).WithArgs("codex", "Original-Device").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM device_bans`).WithArgs("codex", "Original-Device").WillReturnRows(requestAuditWSDeviceBanRows())
	first := []byte(`{"type":"response.create","client_metadata":{"x-codex-installation-id":"Original-Device"}}`)
	require.NoError(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, first))
	// Cancellation of an admitted turn must still pass without another lookup.
	require.NoError(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, []byte(`{"type":"response.cancel"}`)))
	// Rewrites or later metadata cannot replace the first client device.
	second := []byte(`{"type":"response.create","client_metadata":{"x-codex-installation-id":"Rewritten-Device"}}`)
	require.ErrorIs(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, second), errRequestAuditDeviceBlocked)
	require.Equal(t, "Original-Device", ingress.Identity().DeviceID)
}

func TestRequestAuditDeviceWebSocketIgnoresControlMetadata(t *testing.T) {
	ctx, ingress, _ := newRequestAuditDeviceWSContext(t)
	control := []byte(`{"type":"response.cancel","client_metadata":{"x-codex-installation-id":"control"}}`)
	require.NoError(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, control))
	require.Empty(t, ingress.Identity().DeviceID)
}

func TestRequestAuditUnusedDeviceTrackingPreservesWebSocketMessages(t *testing.T) {
	for _, sessionOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "all_off", true: "session_only"}[sessionOnly], func(t *testing.T) {
			cfg := requestaudit.DefaultConfig()
			cfg.SessionBanEnabled = sessionOnly
			ctx, ingress, mock := newRequestAuditDeviceWSContextWithConfig(t, cfg)
			if sessionOnly {
				for range 2 {
					mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "original-session").WillReturnError(sql.ErrNoRows)
				}
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ctx)
			c.Request.Header.Set("session-id", "original-session")
			c.Request.Header.Set("X-Codex-Installation-Id", "Header-Device")
			const original = `{"type":"response.create","client_metadata":{"x-codex-installation-id":"Body-Device","session_id":"body-session"},"input":"unchanged"}`
			payload := []byte(original)
			require.NoError(t, AuditOpenAIWSClientFirstMessage(c, nil, coderws.MessageText, payload))
			require.NoError(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, []byte(`{"type":"response.cancel"}`)))
			require.NoError(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, payload))
			require.Equal(t, original, string(payload))
			identity := ingress.Identity()
			require.Equal(t, "original-session", identity.SessionID)
			require.Empty(t, identity.DeviceType)
			require.Empty(t, identity.DeviceID)
			require.Empty(t, identity.DeviceSource)
			require.Empty(t, identity.DeviceResolution)
		})
	}
}
