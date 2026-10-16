package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestReadGeminiCachedContentBodyPreservesRequestAuditRejection(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/cachedContents", strings.NewReader(`{"model":"gemini-3.8-flash"}`))
	c.Request = c.Request.WithContext(pkghttputil.WithRequestBodyObserver(c.Request.Context(), func([]byte) error {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": 403, "message": "Blocked device"}})
		c.Abort()
		return pkghttputil.ErrBodyObserverRejected
	}))

	body, ok := readGeminiCachedContentBody(c)

	require.False(t, ok)
	require.Nil(t, body)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.JSONEq(t, `{"error":{"code":403,"message":"Blocked device"}}`, recorder.Body.String())
}
