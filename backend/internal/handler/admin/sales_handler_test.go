package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type salesAdminValidationRepoStub struct {
	service.SalesRepository
	saved bool
}

func (r *salesAdminValidationRepoStub) SavePartner(_ context.Context, _ int64, in service.SalesPartnerInput, _ int64) (*service.SalesPartner, error) {
	r.saved = true
	return &service.SalesPartner{ID: 1, UserID: in.UserID, Code: in.Code}, nil
}

func TestSalesAdminCreatePartnerValidationField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, code := range []string{"qq", "qqq", "sales-qq"} {
		t.Run(code, func(t *testing.T) {
			repo := &salesAdminValidationRepoStub{}
			h := NewSalesHandler(service.NewSalesService(repo, nil))
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
				c.Set(string(middleware.ContextKeyUserRole), "admin")
				c.Next()
			})
			router.POST("/partners", h.CreatePartner)
			body := `{"user_id":1,"name":"QQ","code":"` + code + `","hostname":"qq.yusflow.com","commission_rate":50}`
			req := httptest.NewRequest(http.MethodPost, "/partners", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			var result response.Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			if code == "qq" {
				require.Equal(t, http.StatusBadRequest, w.Code)
				require.Equal(t, "SALES_INVALID", result.Reason)
				require.Equal(t, "code", result.Metadata["field"])
				require.False(t, repo.saved)
			} else {
				require.Equal(t, http.StatusOK, w.Code)
				require.True(t, repo.saved)
			}
		})
	}
}

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
