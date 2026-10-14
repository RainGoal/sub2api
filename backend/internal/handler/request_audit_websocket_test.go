package handler

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditCompositeWebSocketGrokSessionAdmission(t *testing.T) {
	for _, tc := range []struct {
		name      string
		blockedAt int
	}{
		{name: "first turn ban", blockedAt: 1},
		{name: "ban after admitted turn", blockedAt: 2},
		{name: "transparent forwarding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			turns := 2
			if tc.blockedAt == 1 {
				turns = 1
			}
			for turn := 1; turn <= turns; turn++ {
				lookup := mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(1801), "original-grok-session")
				if tc.blockedAt == turn {
					lookup.WillReturnRows(sqlmock.NewRows([]string{
						"id", "api_key_id", "session_id", "public_reason", "internal_note", "source_audit_id", "session_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at",
					}).AddRow("ban-id", int64(1801), "original-grok-session", "Blocked session", "", nil, "header.x-grok-conv-id", int64(1), time.Now(), nil, nil, nil))
				} else {
					lookup.WillReturnError(sql.ErrNoRows)
				}
			}
			identity := make(chan requestaudit.Identity, 1)
			middlewares := []gin.HandlerFunc{
				middleware.RequestAuditBegin(manager), middleware.RequestAuditAfterAuth(""), middleware.RequestAuditAfterRouting(""),
				func(c *gin.Context) {
					c.Next()
					identity <- requestaudit.IngressFromContext(c.Request.Context()).Identity()
				},
			}
			result := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				middleware: middlewares, requestHeaders: http.Header{"X-Grok-Conv-Id": {"original-grok-session"}},
				firstPayload:  `{"type":"response.create","model":"public-alias","input":"hello"}`,
				secondPayload: `{"type":"response.create","input":"again"}`,
				group:         compositeWSGroup("public-alias"), accountPlatform: service.PlatformGrok,
				compositeResolver:       compositeWSResolver(service.PlatformGrok, service.CompositeRouteEndpointResponses, "grok-4.3"),
				firstFrameCloseExpected: tc.blockedAt == 1, secondTurnCloseExpected: tc.blockedAt == 2,
				closeErrorCode: "session_banned", closeReason: "session_banned",
			})
			select {
			case id := <-identity:
				require.Equal(t, "original-grok-session", id.SessionID)
				require.Equal(t, "header.x-grok-conv-id", id.SessionSource)
				require.Equal(t, "public-alias", id.Model)
			case <-time.After(3 * time.Second):
				t.Fatal("WebSocket handler did not finish")
			}
			if tc.blockedAt == 0 {
				require.Len(t, result.upstreamPayloads, 2)
				require.Len(t, result.logs, 2)
			}
		})
	}
}
