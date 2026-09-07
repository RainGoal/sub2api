package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthSalesReferralContextAndPendingFirstSource(t *testing.T) {
	for _, test := range []struct {
		name, cookie, pending, want string
		hasPending                  bool
	}{
		{name: "signed-cookie", cookie: "alice.signature", want: "alice.signature"},
		{name: "bare-code-ignored", cookie: "alice"},
		{name: "saved-first-source", cookie: "bob.signature", pending: "alice.signature", hasPending: true, want: "alice.signature"},
		{name: "empty-source-frozen", cookie: "bob.signature", hasPending: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use((&AuthHandler{}).SalesReferralContext())
			router.GET("/test", func(c *gin.Context) {
				if test.hasPending {
					restorePendingSalesReferral(c, map[string]any{oauthSalesReferralStateKey: test.pending})
				}
				require.Equal(t, test.want, service.SalesReferralFromContext(c.Request.Context()))
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "https://main.example.com/test", nil)
			req.AddCookie(&http.Cookie{Name: service.SalesReferralCookieName, Value: test.cookie})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func TestAuthSalesClearCookieOnOAuthTokenResponse(t *testing.T) {
	for _, mode := range []string{"json", "redirect", "error", "no-cookie"} {
		t.Run(mode, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "https://main.example.com/callback", nil)
			if mode != "no-cookie" {
				c.Request.AddCookie(&http.Cookie{Name: service.SalesReferralCookieName, Value: "alice.signature"})
			}
			switch mode {
			case "redirect":
				redirectWithFragment(c, "https://main.example.com/callback", url.Values{"access_token": {"access"}})
			case "error":
				redirectWithFragment(c, "https://main.example.com/callback", url.Values{"error": {"invalid_state"}})
			default:
				writeOAuthTokenPairResponse(c, &service.TokenPair{AccessToken: "access"})
			}
			cookie := findCookie(recorder.Result().Cookies(), service.SalesReferralCookieName)
			if mode == "error" || mode == "no-cookie" {
				require.Nil(t, cookie)
				return
			}
			require.NotNil(t, cookie)
			require.Equal(t, -1, cookie.MaxAge)
			require.True(t, cookie.HttpOnly)
			require.True(t, cookie.Secure)
			require.Empty(t, cookie.Domain)
		})
	}
}

func TestAuthSalesOAuthStatePreventsChangedSourceAndReplay(t *testing.T) {
	const secret = "test-oauth-sales-state-secret"
	h := &AuthHandler{cfg: &config.Config{JWT: config.JWTConfig{Secret: secret}}}
	for _, mode := range []string{"valid", "wrong-state", "expired", "tampered", "missing"} {
		t.Run(mode, func(t *testing.T) {
			saved := salesOAuthReferral{State: "original-state", Referral: "alice.signature", ExpiresAt: time.Now().Add(time.Minute).Unix()}
			state := saved.State
			if mode == "wrong-state" {
				state = "other-state"
			}
			if mode == "expired" {
				saved.ExpiresAt = time.Now().Add(-time.Minute).Unix()
			}
			value := encodeSalesOAuthReferral(secret, saved)
			if mode == "tampered" {
				value += "x"
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "https://main.example.com/api/v1/auth/oauth/google/callback", nil)
			c.Request = c.Request.WithContext(service.ContextWithSalesReferral(c.Request.Context(), "bob.signature"))
			if mode != "missing" {
				c.Request.AddCookie(&http.Cookie{Name: "sales_oauth_google", Value: value})
			}
			h.restoreSalesOAuthReferral(c, "google", state)
			want := ""
			if mode == "valid" {
				want = "alice.signature"
			}
			require.Equal(t, want, service.SalesReferralFromContext(c.Request.Context()))
		})
	}
}
