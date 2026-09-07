package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSalesAdminRejectsOrdinaryUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewSalesHandler(nil)
	for _, endpoint := range []struct {
		method, path string
		handler      gin.HandlerFunc
	}{{http.MethodGet, "/settings", h.GetSettings}, {http.MethodPut, "/settings", h.SaveSettings}, {http.MethodPost, "/partners", h.CreatePartner}, {http.MethodGet, "/customers", h.Customers}, {http.MethodPost, "/settlements", h.CreateSettlement}, {http.MethodPost, "/settlements/:id/pay", h.PaySettlement}, {http.MethodPost, "/adjustments", h.Adjust}} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
			c.Set(string(middleware.ContextKeyUserRole), "user")
			c.Next()
		})
		router.Handle(endpoint.method, endpoint.path, endpoint.handler)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		require.Equal(t, http.StatusForbidden, w.Code, endpoint.path)
	}
}
