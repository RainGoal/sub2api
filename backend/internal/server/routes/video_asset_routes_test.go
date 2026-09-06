package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoAssetRoutesUseUserAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, authenticated := range []bool{false, true} {
		router := gin.New()
		audited := 0
		RegisterUserRoutes(router.Group("/api/v1"), &handler.Handlers{
			VideoAsset: handler.NewVideoAssetHandler(service.NewVideoAssetService(nil, nil), nil),
		}, func(c *gin.Context) {
			if !authenticated {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		}, func(c *gin.Context) { audited++; c.Next() }, nil, nil)
		for _, tc := range []struct {
			method, path      string
			authenticatedCode int
		}{
			{http.MethodGet, "/api/v1/video-assets/config", 200},
			{http.MethodPost, "/api/v1/video-assets", 403},
		} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if authenticated {
				require.Equal(t, tc.authenticatedCode, w.Code)
			} else {
				require.Equal(t, 401, w.Code)
			}
		}
		if authenticated {
			require.Equal(t, 2, audited)
		} else {
			require.Zero(t, audited)
		}
	}
}
