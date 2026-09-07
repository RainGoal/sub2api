package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type salesHandlerRepoStub struct {
	service.SalesRepository
	filter   service.SalesFilter
	settings service.SalesSettings
}

func (r *salesHandlerRepoStub) GetPartnerByUser(_ context.Context, userID int64) (*service.SalesPartner, error) {
	if userID != 7 {
		return nil, service.ErrSalesNotFound
	}
	return &service.SalesPartner{ID: 42, UserID: 7}, nil
}
func (r *salesHandlerRepoStub) ListCustomers(_ context.Context, f service.SalesFilter) ([]service.SalesCustomer, int64, error) {
	r.filter = f
	return []service.SalesCustomer{{UserID: 9, PartnerID: 42, Email: "private@example.com"}}, 1, nil
}
func (r *salesHandlerRepoStub) GetSettings(context.Context) (*service.SalesSettings, error) {
	return &r.settings, nil
}
func (r *salesHandlerRepoStub) GetPartnerByCode(_ context.Context, code string) (*service.SalesPartner, error) {
	if code == "sales-b" {
		return &service.SalesPartner{ID: 43, Code: code, PromotionEnabled: true}, nil
	}
	if code != "sales-a" {
		return nil, service.ErrSalesNotFound
	}
	return &service.SalesPartner{ID: 42, Code: "sales-a", PromotionEnabled: true}, nil
}

func TestSalesCustomersUsesAuthenticatedOwnerAndMasksIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &salesHandlerRepoStub{}
	h := NewSalesHandler(service.NewSalesService(repo, nil))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	router.GET("/sales/customers", h.Customers)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sales/customers?partner_id=999&page=1&page_size=20", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.EqualValues(t, 42, repo.filter.PartnerID)
	require.Contains(t, w.Body.String(), "p***@example.com")
	require.NotContains(t, w.Body.String(), "private@example.com")
}

func TestSalesSelfRequiresAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/sales/me", NewSalesHandler(nil).Me)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sales/me", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSalesReferralCookieAndRedirectUseCanonicalOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &salesHandlerRepoStub{settings: service.SalesSettings{Enabled: true, MainFrontendURL: "https://main.example.com"}}
	h := NewSalesHandler(service.NewSalesService(repo, &config.Config{JWT: config.JWTConfig{Secret: strings.Repeat("k", 32)}}))
	router := gin.New()
	router.GET("/api/v1/sales/referral", h.Referral)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://main.example.com/api/v1/sales/referral?code=sales-a&redirect=https://evil.example", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "https://main.example.com/register", w.Header().Get("Location"))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.True(t, cookies[0].HttpOnly)
	require.True(t, cookies[0].Secure)
	require.Empty(t, cookies[0].Domain)
	require.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "https://evil.example/api/v1/sales/referral?code=sales-a", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, "https://main.example.com/api/v1/sales/referral?code=sales-a", w.Header().Get("Location"))
	require.Empty(t, w.Result().Cookies())

	// A second valid promotion must not change or extend the first source.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "https://main.example.com/api/v1/sales/referral?code=sales-b", nil)
	req.AddCookie(cookies[0])
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "https://main.example.com/register", w.Header().Get("Location"))
	require.Empty(t, w.Result().Cookies())
}
