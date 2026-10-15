package middleware

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditCodexSessionBanBeforeRewrite(t *testing.T) {
	for _, tc := range []struct{ name, header, body string }{
		{"native header", `{"session_id":"codex-session","thread_id":"codex-session"}`, `{"model":"client-model"}`},
		{"body session", "", `{"model":"client-model","client_metadata":{"session_id":"codex-session"}}`},
		{"body turn metadata", "", `{"model":"client-model","client_metadata":{"x-codex-turn-metadata":"{\"session_id\":\"codex-session\"}"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, mock := newRequestAuditPolicyManager(t)
			mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "codex-session").WillReturnRows(requestAuditBanRows("codex-session"))
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth("", 4096))
			router.POST("/v1/responses", func(c *gin.Context) {
				require.Empty(t, tc.header, "header ban must run before the handler reads the body")
				_, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.ErrorIs(t, err, httputil.ErrBodyObserverRejected)
				require.Equal(t, "codex-session", requestaudit.IngressFromContext(c.Request.Context()).Identity().SessionID)
			})
			body := bytes.NewBufferString(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
			req.Header.Set("X-Codex-Turn-Metadata", tc.header)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code)
			if tc.header != "" {
				require.Equal(t, tc.body, body.String())
			}
		})
	}
}

func TestRequestAuditCodexCompositeGrokHeaderPriority(t *testing.T) {
	for _, tc := range []struct{ platform, header string }{
		{service.PlatformGrok, `{"session_id":"codex-header"}`},
		{service.PlatformOpenAI, `{"session_id":"codex-header"}`},
		{service.PlatformGrok, ""},
		{service.PlatformOpenAI, ""},
	} {
		t.Run(tc.platform+tc.header, func(t *testing.T) {
			manager, mock := newRequestAuditPolicyManager(t)
			wantSession := "codex-body"
			if tc.header != "" {
				wantSession = "codex-header"
			}
			if tc.platform == service.PlatformGrok {
				wantSession = "grok-session"
			}
			mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), wantSession).WillReturnRows(requestAuditBanRows(wantSession))
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth(""), func(c *gin.Context) {
				_, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err, "metadata admission waits for the higher-priority Grok header")
				c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), tc.platform))
				c.Next()
			}, RequestAuditAfterRouting(""))
			router.POST("/v1/responses", func(c *gin.Context) { t.Error("banned request reached handler") })
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"client_metadata":{"session_id":"codex-body"}}`))
			req.Header.Set("x-grok-conv-id", "grok-session")
			req.Header.Set("X-Codex-Turn-Metadata", tc.header)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

func TestRequestAuditCodexBodyReadPreservesBytesAndHeaderPriority(t *testing.T) {
	const original = `{"model":"original","client_metadata":{"session_id":"body-session"}}`
	for _, tc := range []struct {
		name, header, session string
		gzip                  bool
		limit                 int64
	}{
		{name: "original body", session: "body-session", limit: 4096},
		{name: "gzip body", gzip: true, session: "body-session", limit: 4096},
		{name: "ordinary header wins", header: "header-session", session: "header-session", limit: 4096},
		{name: "body over parsing limit", limit: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, mock := newRequestAuditPolicyManager(t)
			if tc.session != "" {
				mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), tc.session).WillReturnError(sql.ErrNoRows)
			}
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth("", tc.limit))
			router.POST("/v1/responses", func(c *gin.Context) {
				body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err)
				require.Equal(t, original, string(body))
				require.Equal(t, tc.session, requestaudit.IngressFromContext(c.Request.Context()).Identity().SessionID)
				c.Status(http.StatusNoContent)
			})
			var raw bytes.Buffer
			if tc.gzip {
				writer := gzip.NewWriter(&raw)
				_, err := writer.Write([]byte(original))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
			} else {
				_, err := raw.WriteString(original)
				require.NoError(t, err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", &raw)
			req.Header.Set("session-id", tc.header)
			if tc.gzip {
				req.Header.Set("Content-Encoding", "gzip")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code)
		})
	}
}
