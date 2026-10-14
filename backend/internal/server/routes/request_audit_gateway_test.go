package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditMountedBeforeAuthenticationOnEveryGatewayRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var seen bool
	router.Use(func(c *gin.Context) {
		c.Next()
		seen = requestaudit.IngressFromContext(c.Request.Context()) != nil
	})
	RegisterGatewayRoutes(router, &handler.Handlers{
		Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{},
		AsyncImage: handler.NewAsyncImageHandler(nil, nil), RequestAudit: requestaudit.NewManager(nil),
	}, middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		require.NotNil(t, requestaudit.IngressFromContext(c.Request.Context()), "audit must run before authentication rejects")
		c.AbortWithStatus(http.StatusUnauthorized)
	}), nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024, TextMaxBodySize: 512}})
	for _, route := range router.Routes() {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			parts := strings.Split(route.Path, "/")
			for i, part := range parts {
				if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
					parts[i] = "example"
				}
			}
			seen = false
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(route.Method, strings.Join(parts, "/"), nil))
			require.True(t, seen, "gateway request was not observed before auth rejection")
			require.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}
