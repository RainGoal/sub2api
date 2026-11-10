//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPlazaMonitorGeminiVerifiesProbeMarker(t *testing.T) {
	for _, signed := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", nil)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{Key: "secret"})
		if signed {
			c.Request.Header.Set(service.ModelPlazaMonitorProbeHeaderName, service.BuildChannelMonitorProbeHeader("secret", time.Now()))
		}
		// Missing auth subject exits after the marker is verified, before routing.
		(&GatewayHandler{}).GeminiV1BetaModels(c)
		require.Equal(t, signed, service.IsChannelMonitorProbe(c.Request.Context()))
		require.Equal(t, signed, service.IsModelPlazaMonitorProbe(c.Request.Context()))
	}
}
