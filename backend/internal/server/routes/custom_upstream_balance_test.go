package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamBalanceRoutesUseAdminGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{UpstreamBalance: adminhandler.NewUpstreamBalanceHandler(nil)}}
	auditCalls := 0
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		switch c.GetHeader("Authorization") {
		case "Bearer admin-token":
			c.Next()
		case "":
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
		default:
			servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
		}
	})
	audit := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { auditCalls++; c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, audit, stepUp, nil, nil)

	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/upstream-balances"},
		{http.MethodGet, "/api/v1/admin/upstream-balances/discovery"},
		{http.MethodPut, "/api/v1/admin/upstream-balances/config"},
		{http.MethodPost, "/api/v1/admin/upstream-balances/626cf342-4208-49ae-8bc3-47eb48aba1ec/refresh"},
	} {
		for _, tc := range []struct {
			auth string
			want int
		}{
			{"", http.StatusUnauthorized},
			{"Bearer user-token", http.StatusForbidden},
			{"Bearer admin-token", http.StatusServiceUnavailable},
		} {
			t.Run(endpoint.method+endpoint.path+tc.auth, func(t *testing.T) {
				before := auditCalls
				w := httptest.NewRecorder()
				req := httptest.NewRequest(endpoint.method, endpoint.path, nil)
				req.Header.Set("Authorization", tc.auth)
				router.ServeHTTP(w, req)
				require.Equal(t, tc.want, w.Code)
				if tc.auth == "Bearer admin-token" {
					require.Equal(t, before+1, auditCalls)
					require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				} else {
					require.Equal(t, before, auditCalls)
				}
			})
		}
	}
}
