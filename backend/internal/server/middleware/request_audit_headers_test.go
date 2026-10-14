package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditPreservesInboundHeadersBeforeSessionBindingNormalization(t *testing.T) {
	for _, values := range [][]string{
		{strings.Repeat("u", 600)}, {"first-agent", "second-agent"}, nil,
		{""}, {"regular-agent"}, {"non-utf8-\xff"},
	} {
		t.Run(strings.Join(values, ","), func(t *testing.T) {
			router := gin.New()
			router.Use(SessionBindingContext(&config.Config{}))
			request := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			if values != nil {
				request.Header["User-Agent"] = values
			}
			request.Header["X-Custom"] = []string{"first", "second"}
			original := request.Header.Clone()
			router.POST("/v1/messages", func(c *gin.Context) {
				binding := service.SessionBindingFromContext(c.Request.Context())
				require.Equal(t, binding.UserAgent, c.Request.UserAgent())
				require.LessOrEqual(t, len(binding.UserAgent), maxPersistentUserAgentBytes)
				require.Equal(t, original, requestAuditInboundHeaders(c, true))
				require.Equal(t, binding.UserAgent, c.Request.UserAgent(), "audit must not alter the business headers")
				retained, _ := c.Get(requestAuditUserAgentKey)
				require.Nil(t, retained, "original values must not outlive capture outside its memory budget")
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func TestRequestAuditDisabledCaptureReleasesOriginalHeaders(t *testing.T) {
	for _, manager := range []*requestaudit.Manager{nil, requestaudit.NewManager(nil)} {
		router := gin.New()
		router.Use(SessionBindingContext(&config.Config{}), RequestAuditBegin(manager))
		router.GET("/v1/responses", func(c *gin.Context) {
			retained, _ := c.Get(requestAuditUserAgentKey)
			require.Nil(t, retained)
			require.Len(t, c.Request.UserAgent(), maxPersistentUserAgentBytes)
			c.Status(http.StatusNoContent)
		})
		request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		request.Header.Set("User-Agent", strings.Repeat("u", 600))
		router.ServeHTTP(httptest.NewRecorder(), request)
	}
}
