package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeviceBanAdminRoutesRequireAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	admin.Use(func(c *gin.Context) {
		if role := c.GetHeader("X-Test-Role"); role != "" {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
	})
	registerRequestAuditRoutes(admin, &handler.Handlers{Admin: &handler.AdminHandlers{RequestAudit: adminhandler.NewRequestAuditHandler(nil)}})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/device-bans"},
		{http.MethodPost, "/api/v1/admin/device-bans"},
		{http.MethodGet, "/api/v1/admin/device-bans/693b5cb0-cdef-4acf-a2e6-3f760debb1ee"},
		{http.MethodPost, "/api/v1/admin/device-bans/693b5cb0-cdef-4acf-a2e6-3f760debb1ee/revoke"},
	} {
		for role, status := range map[string]int{"": http.StatusUnauthorized, "user": http.StatusForbidden, "admin": http.StatusServiceUnavailable} {
			t.Run(route.method+" "+route.path+" "+role, func(t *testing.T) {
				request := httptest.NewRequest(route.method, route.path, nil)
				request.Header.Set("X-Test-Role", role)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, request)
				require.Equal(t, status, w.Code)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			})
		}
	}
}
