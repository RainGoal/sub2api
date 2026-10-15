package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditDeviceWebSocketBlockDoesNotPenalizeAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(requestaudit.ConfigKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":false,"retention_days":3,"session_ban_enabled":false,"device_ban_enabled":true,"version":1}`))
	manager := requestaudit.NewManager(db)
	require.NoError(t, manager.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, manager.Stop(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	mock.ExpectQuery(`FROM device_bans`).WithArgs("codex", "original-client-device").WillReturnRows(sqlmock.NewRows([]string{
		"id", "device_type", "device_id", "public_reason", "internal_note", "source_audit_id", "device_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at",
	}).AddRow("device-ban", "codex", "original-client-device", "Blocked device", "", nil, "header", int64(1), time.Now(), nil, nil, nil))
	ingress := requestaudit.BeginIngress(manager, requestaudit.RequestMeta{})
	ingress.BindIdentity(21, 12, 0)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).
		WithContext(requestaudit.WithIngress(context.Background(), ingress))
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"installation_id":"original-client-device"}`)
	err = service.AuditOpenAIWSClientFirstMessage(c, nil, coderws.MessageText, []byte(`{"type":"response.create"}`))
	require.Error(t, err)
	for _, blocked := range []error{err, fmt.Errorf("read client websocket request: %w", err)} {
		var closeErr *service.OpenAIWSClientCloseError
		require.ErrorAs(t, blocked, &closeErr)
		require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
		require.True(t, service.IsRequestAuditDeviceBlockedError(blocked))
		require.False(t, shouldReportOpenAIWSProxyAccountFailure(blocked), "a local device policy must not lower the selected account's success rate")
	}
	// Neither the close code nor matching text identifies the local sentinel.
	upstream := service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "upstream websocket authentication failed", errors.New("device blocked"))
	require.False(t, service.IsRequestAuditDeviceBlockedError(upstream))
	require.True(t, shouldReportOpenAIWSProxyAccountFailure(upstream))
}

func TestRequestAuditDeviceWebSocketRejectsBeforeUpstreamForwarding(t *testing.T) {
	for _, blockedAt := range []int{-1, 0, 1, 2} {
		t.Run(fmt.Sprintf("blocked_at_%d", blockedAt), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(requestaudit.ConfigKey).
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(fmt.Sprintf(`{"enabled":false,"retention_days":3,"session_ban_enabled":false,"device_ban_enabled":%t,"version":1}`, blockedAt >= 0)))
			manager := requestaudit.NewManager(db)
			require.NoError(t, manager.Start(context.Background()))
			t.Cleanup(func() {
				require.NoError(t, manager.Stop(context.Background()))
				require.NoError(t, mock.ExpectationsWereMet())
				_ = db.Close()
			})
			turns := 2
			if blockedAt == 1 {
				turns = 1
			}
			for turn := 1; blockedAt >= 0 && turn <= turns; turn++ {
				query := mock.ExpectQuery(`FROM device_bans`).WithArgs("codex", "original-client-device")
				if blockedAt == turn {
					query.WillReturnRows(sqlmock.NewRows([]string{
						"id", "device_type", "device_id", "public_reason", "internal_note", "source_audit_id", "device_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at",
					}).AddRow("device-ban", "codex", "original-client-device", "Blocked device", "", nil, "body", int64(1), time.Now(), nil, nil, nil))
				} else {
					query.WillReturnError(sql.ErrNoRows)
				}
			}
			identity := make(chan requestaudit.Identity, 1)
			result := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				middleware: []gin.HandlerFunc{
					middleware.RequestAuditBegin(manager), middleware.RequestAuditAfterAuth(""), middleware.RequestAuditAfterRouting(""),
					func(c *gin.Context) {
						c.Next()
						identity <- requestaudit.IngressFromContext(c.Request.Context()).Identity()
					},
				},
				firstPayload:  `{"type":"response.create","model":"public-alias","input":"hello","client_metadata":{"x-codex-installation-id":"original-client-device"}}`,
				secondPayload: `{"type":"response.create","input":"again","client_metadata":{"x-codex-installation-id":"other-device"}}`,
				group:         compositeWSGroup("public-alias"), accountPlatform: service.PlatformGrok,
				compositeResolver:       compositeWSResolver(service.PlatformGrok, service.CompositeRouteEndpointResponses, "grok-4.3"),
				firstFrameCloseExpected: blockedAt == 1, secondTurnCloseExpected: blockedAt == 2,
				closeErrorCode: "device_banned", closeReason: "device_banned",
			})
			select {
			case id := <-identity:
				if blockedAt < 0 {
					require.Empty(t, id.DeviceID)
					require.Empty(t, id.DeviceType)
				} else {
					require.Equal(t, "original-client-device", id.DeviceID)
					require.Equal(t, "codex", id.DeviceType)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("WebSocket handler did not finish")
			}
			if blockedAt <= 0 {
				require.Len(t, result.upstreamPayloads, 2)
				require.Len(t, result.logs, 2)
			}
		})
	}
}
