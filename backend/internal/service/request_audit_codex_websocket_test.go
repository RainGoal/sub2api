package service

import (
	"context"
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

func newRequestAuditCodexWSContext(t *testing.T) (context.Context, *requestaudit.Ingress, sqlmock.Sqlmock) {
	t.Helper()
	ctx, manager, mock := newRequestAuditWSPolicyContext(t)
	ingress := requestaudit.BeginIngress(manager, requestaudit.RequestMeta{})
	ingress.BindIdentity(21, 12, 1)
	return requestaudit.WithIngress(ctx, ingress), ingress, mock
}

func TestRequestAuditCodexWebSocketFirstMessageBan(t *testing.T) {
	for _, tc := range []struct {
		name, body, header, explicit, platform, session, source string
	}{
		{name: "native header", header: `{"session_id":"native","thread_id":"native"}`, body: `{"type":"response.create","model":"original"}`, session: "native", source: "header.x-codex-turn-metadata.session_id"},
		{name: "ordinary header first", explicit: "ordinary", header: `{"session_id":"native"}`, body: `{"type":"response.create","client_metadata":{"session_id":"body"}}`, session: "ordinary", source: "header.session-id"},
		{name: "direct body", body: `{"type":"response.create","client_metadata":{"session_id":"body"}}`, session: "body", source: "body.client_metadata.session_id"},
		{name: "envelope", body: `{"type":"response.create","response":{"client_metadata":{"session_id":"body"}}}`, session: "body", source: "body.client_metadata.session_id"},
		{name: "embedded envelope", body: `{"type":"response.create","response":{"client_metadata":{"x-codex-turn-metadata":"{\"session_id\":\"body\"}"}}}`, session: "body", source: "body.client_metadata.x-codex-turn-metadata.session_id"},
		{name: "resolved Grok header first", platform: PlatformGrok, header: `{"session_id":"native"}`, body: `{"type":"response.create","client_metadata":{"session_id":"body"}}`, session: "grok-session", source: "header.x-grok-conv-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, ingress, mock := newRequestAuditCodexWSContext(t)
			mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), tc.session).WillReturnRows(sqlmock.NewRows([]string{
				"id", "api_key_id", "session_id", "public_reason", "internal_note", "source_audit_id", "session_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at",
			}).AddRow("ban-id", int64(12), tc.session, "Public reason", "private", nil, tc.source, int64(1), time.Now(), nil, nil, nil))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ctx)
			c.Request.Header.Set("X-Codex-Turn-Metadata", tc.header)
			c.Request.Header.Set("session-id", tc.explicit)
			if tc.platform != "" {
				c.Request = c.Request.WithContext(WithResolvedTargetPlatform(ctx, tc.platform))
				c.Request.Header.Set("X-Grok-Conv-Id", "grok-session")
			}
			payload := []byte(tc.body)
			err := AuditOpenAIWSClientFirstMessage(c, nil, coderws.MessageText, payload)
			require.ErrorIs(t, err, errRequestAuditSessionBlocked)
			require.Equal(t, tc.session, ingress.Identity().SessionID)
			require.Equal(t, tc.source, ingress.Identity().SessionSource)
			require.Equal(t, tc.body, string(payload))
		})
	}
}

func TestRequestAuditCodexWebSocketOnlyBindsResponsesGeneration(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.create","prompt_cache_key":"cache","client_metadata":{"thread_id":"thread","turn_id":"turn","window_id":"window"}}`,
		`{"type":"response.cancel","client_metadata":{"session_id":"control"}}`,
		`{"type":"response.create","client_metadata":{"session_id":123}}`,
		`{"type":"response.create","client_metadata":{"session_id":"broken"}`,
	} {
		ctx, ingress, _ := newRequestAuditCodexWSContext(t)
		require.NoError(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, []byte(payload)))
		require.Empty(t, ingress.Identity().SessionID)
	}
	ctx, ingress, mock := newRequestAuditCodexWSContext(t)
	// Generic Realtime/Live metadata is outside the Responses fallback scope.
	const payload = `{"type":"response.create","client_metadata":{"session_id":"later"}}`
	require.NoError(t, auditGatewayClientWSMessage(ctx, nil, coderws.MessageText, []byte(payload)))
	require.Empty(t, ingress.Identity().SessionID)
	mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "later").WillReturnRows(sqlmock.NewRows([]string{
		"id", "api_key_id", "session_id", "public_reason", "internal_note", "source_audit_id", "session_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at",
	}).AddRow("ban-id", int64(12), "later", "Public reason", "private", nil, "body.client_metadata.session_id", int64(1), time.Now(), nil, nil, nil))
	require.ErrorIs(t, auditGatewayResponsesWSMessage(ctx, nil, coderws.MessageText, []byte(payload)), errRequestAuditSessionBlocked)
	require.Equal(t, "later", ingress.Identity().SessionID)
}
