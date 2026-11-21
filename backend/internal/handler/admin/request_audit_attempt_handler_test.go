package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type requestAuditAttemptStore struct {
	RequestAuditAdminStore
	list func(string) ([]requestaudit.AttemptRecord, error)
	read func(string, int, string, int64, int64, bool) (requestaudit.PayloadResult, error)
}

func (s requestAuditAttemptStore) ListAttempts(_ context.Context, id string) ([]requestaudit.AttemptRecord, error) {
	return s.list(id)
}

func (s requestAuditAttemptStore) ReadAttemptPayload(_ context.Context, id string, attempt int, part string, offset, limit int64, partial bool) (requestaudit.PayloadResult, error) {
	return s.read(id, attempt, part, offset, limit, partial)
}

func TestRequestAuditAttemptsList(t *testing.T) {
	h := &RequestAuditHandler{store: requestAuditAttemptStore{list: func(id string) ([]requestaudit.AttemptRecord, error) {
		require.Equal(t, testRequestAuditID, id)
		return nil, nil
	}}}
	router := requestAuditTestRouter("admin", h.ListAttempts)
	w := requestAuditTestRequest(router, http.MethodGet, "", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"items":[]}}`, w.Body.String())
}

func TestRequestAuditAttemptPayloadPreviewAndDownload(t *testing.T) {
	source := []byte(`{"headers":{"Authorization":["Bearer captured-value"]}}`)
	h := &RequestAuditHandler{reads: make(chan struct{}, 2), store: requestAuditAttemptStore{
		read: func(id string, attempt int, part string, offset, limit int64, partial bool) (requestaudit.PayloadResult, error) {
			require.Equal(t, testRequestAuditID, id)
			require.Equal(t, 2, attempt)
			require.Equal(t, "request_headers", part)
			require.True(t, partial)
			end := min(int64(len(source)), offset+limit)
			return requestaudit.PayloadResult{Bytes: source[offset:end], TotalBytes: int64(len(source)), HasMore: end < int64(len(source)), CaptureState: "complete"}, nil
		},
	}}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
	})
	router.GET("/audit/:id/attempts/:attempt/payload", h.AttemptPayload)
	path := "/audit/" + testRequestAuditID + "/attempts/2/payload"
	request := httptest.NewRequest(http.MethodGet, path+"?part=request_headers&allow_partial=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"redacted":false`)
	require.Contains(t, w.Body.String(), "Bearer captured-value")

	request = httptest.NewRequest(http.MethodGet, path+"?part=request_headers&allow_partial=true&mode=raw", nil)
	request.Header.Set("Range", "bytes=0-1")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, http.StatusPartialContent, w.Code)
	require.Equal(t, string(source[:2]), w.Body.String())
	require.Contains(t, w.Header().Get("Content-Disposition"), "attempt-2-request_headers")

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?part=headers", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}
