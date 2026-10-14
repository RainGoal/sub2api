package middleware

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newRequestAuditPolicyManager(t *testing.T) (*requestaudit.Manager, sqlmock.Sqlmock) {
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
	return manager, mock
}

func requestAuditBanRows(session string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "api_key_id", "session_id", "public_reason", "internal_note", "source_audit_id", "session_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at"}).
		AddRow("ban-id", int64(12), session, "Public moderation reason", "private operator note", nil, "header.session-id", int64(1), time.Now(), nil, nil, nil)
}

func requestAuditTestAuth(c *gin.Context) {
	c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 12, UserID: 21, Group: &service.Group{Platform: service.PlatformComposite}})
	c.Next()
}

func TestRequestAuditHeaderBanWorksWithoutCaptureAndDoesNotConsumeBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ path, protocol, errorType string }{
		{"/v1/responses", "", "session_banned"},
		{"/v1/messages", "", "permission_error"},
		{"/v1beta/models/model:generateContent", "google", "PERMISSION_DENIED"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			manager, mock := newRequestAuditPolicyManager(t)
			mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "original-session").WillReturnRows(requestAuditBanRows("original-session"))
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth(tc.protocol))
			router.POST(tc.path, func(c *gin.Context) { t.Error("banned request reached upstream handler") })
			body := bytes.NewBufferString(`{"original":true}`)
			req := httptest.NewRequest(http.MethodPost, tc.path, body)
			req.Header.Set("session-id", "original-session")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Contains(t, w.Body.String(), tc.errorType)
			require.Contains(t, w.Body.String(), "Public moderation reason")
			require.NotContains(t, w.Body.String(), "private operator note")
			require.Equal(t, `{"original":true}`, body.String())
			require.Zero(t, manager.Status().AcceptedRequests)
		})
	}
}

func TestRequestAuditClaudeBodyBanRunsBeforeRewrite(t *testing.T) {
	manager, mock := newRequestAuditPolicyManager(t)
	const original = `{"metadata":{"user_id":"{\"device_id\":\"device\",\"session_id\":\"body-session\"}"},"model":"client-model"}`
	mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "body-session").WillReturnRows(requestAuditBanRows("body-session"))
	var raw bytes.Buffer
	compressor := gzip.NewWriter(&raw)
	_, err := compressor.Write([]byte(original))
	require.NoError(t, err)
	require.NoError(t, compressor.Close())
	router := gin.New()
	router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth(""))
	router.POST("/v1/messages", func(c *gin.Context) {
		_, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		require.ErrorIs(t, err, httputil.ErrBodyObserverRejected)
		require.True(t, IsRequestAuditBodyObserverError(err))
		require.Equal(t, "body-session", requestaudit.IngressFromContext(c.Request.Context()).Identity().SessionID)
		require.Equal(t, "client-model", requestaudit.IngressFromContext(c.Request.Context()).Identity().Model)
		require.Equal(t, "gzip", c.Request.Header.Get("Content-Encoding"))
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", &raw)
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestRequestAuditHeaderAdmissionIsCheckedOnceEvenAfterReadAndRouting(t *testing.T) {
	manager, mock := newRequestAuditPolicyManager(t)
	// A lookup failure must fail open once; it must not cause another read-time
	// query to reject a request which already passed its admission decision.
	mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "header-session").WillReturnError(errors.New("policy storage unavailable"))
	router := gin.New()
	router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth(""), RequestAuditAfterRouting(""))
	router.POST("/v1/messages", func(c *gin.Context) {
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		c.Request.Body = httputil.NewPrereadBody(body)
		_, err = httputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		require.Equal(t, "header-session", requestaudit.IngressFromContext(c.Request.Context()).Identity().SessionID)
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"metadata":{"user_id":"{\"device_id\":\"device\",\"session_id\":\"body-session\"}"}}`))
	req.Header.Set("session-id", "header-session")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.EqualValues(t, 1, manager.Status().BanCheckFailures)
}

func TestRequestAuditGrokSessionResolvesAfterCompositeRouting(t *testing.T) {
	manager, mock := newRequestAuditPolicyManager(t)
	mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "grok-session").WillReturnRows(requestAuditBanRows("grok-session"))
	router := gin.New()
	router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth(""), func(c *gin.Context) {
		c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformGrok))
		c.Next()
	}, RequestAuditAfterRouting(""))
	router.POST("/v1/responses", func(c *gin.Context) { t.Error("banned composite request reached handler") })
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("x-grok-conv-id", "grok-session")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestRequestAuditCompositeHeaderPrecedesClaudeBodyAfterRouting(t *testing.T) {
	for _, platform := range []string{service.PlatformGrok, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			manager, mock := newRequestAuditPolicyManager(t)
			wantSession := "body-session"
			if platform == service.PlatformGrok {
				wantSession = "grok-session"
			}
			mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), wantSession).WillReturnRows(requestAuditBanRows(wantSession))
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth(""), func(c *gin.Context) {
				body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err, "body fallback admission must wait for a potential higher-priority Grok header")
				c.Request.Body = httputil.NewPrereadBody(body)
				c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), platform))
				c.Next()
			}, RequestAuditAfterRouting(""))
			router.POST("/v1/messages", func(c *gin.Context) { t.Error("banned composite request reached handler") })
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"metadata":{"user_id":"{\"device_id\":\"device\",\"session_id\":\"body-session\"}"}}`))
			req.Header.Set("x-grok-conv-id", "grok-session")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

func TestRequestAuditDisabledManagerPreservesReadAndWriter(t *testing.T) {
	router := gin.New()
	router.Use(RequestAuditBegin(requestaudit.NewManager(nil)), requestAuditTestAuth, RequestAuditAfterAuth(""))
	router.POST("/v1/messages", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.Status(http.StatusOK)
		_, err = c.Writer.Write(body)
		require.NoError(t, err)
		c.Writer.Flush()
	})
	payload := []byte{0, 0xff, '\r', '\n', '{', '}'}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(payload)))
	require.Equal(t, payload, w.Body.Bytes())
	require.True(t, w.Flushed)
}

func TestRequestAuditEmptyGetAndWebSocketHandshakePreserveNoBody(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		router := gin.New()
		router.Use(RequestBodyLimit(1024), RequestAuditBegin(requestaudit.NewManager(nil)))
		router.GET("/v1/responses", func(c *gin.Context) {
			require.Equal(t, http.NoBody, c.Request.Body, "known empty body must remain distinguishable from an unread stream")
			require.NotNil(t, requestaudit.IngressFromContext(c.Request.Context()))
			c.Status(http.StatusNoContent)
		})
		req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		if websocket {
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Connection", "Upgrade")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code)
	}
}

func TestRequestAuditDoesNotTreatZeroContentLengthAsEmptyBody(t *testing.T) {
	router := gin.New()
	router.Use(RequestBodyLimit(1024), RequestAuditBegin(requestaudit.NewManager(nil)))
	router.POST("/v1/responses", func(c *gin.Context) {
		require.NotEqual(t, http.NoBody, c.Request.Body)
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, "still has bytes", string(body))
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Body = io.NopCloser(bytes.NewBufferString("still has bytes"))
	req.ContentLength = 0
	router.ServeHTTP(httptest.NewRecorder(), req)
}
