package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const oauthSalesReferralStateKey = "sales_referral"

// SalesReferralContext carries the main-domain referral through every auth
// route. Verification is deferred until new-account creation so an invalid or
// unavailable promotion cannot alter an existing user's login.
func (h *AuthHandler) SalesReferralContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		if cookie, err := c.Cookie(service.SalesReferralCookieName); err == nil {
			if strings.Count(cookie, ".") == 1 && len(cookie) <= 1024 {
				c.Request = c.Request.WithContext(service.ContextWithSalesReferral(c.Request.Context(), cookie))
			}
		}
		c.Next()
	}
}

func clearSalesReferralCookie(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	for _, name := range []string{service.SalesReferralCookieName, "sales_oauth_google", "sales_oauth_github", "sales_oauth_linuxdo", "sales_oauth_oidc", "sales_oauth_wechat", "sales_oauth_dingtalk"} {
		if _, err := c.Request.Cookie(name); err != nil {
			continue
		}
		path := "/api/v1/auth"
		if name == service.SalesReferralCookieName {
			path = "/"
		}
		http.SetCookie(c.Writer, &http.Cookie{
			Name: name, Value: "", Path: path,
			MaxAge: -1, HttpOnly: true, Secure: isRequestHTTPS(c), SameSite: http.SameSiteLaxMode,
		})
	}
}

func restorePendingSalesReferral(c *gin.Context, localFlowState map[string]any) {
	if referral, ok := localFlowState[oauthSalesReferralStateKey].(string); ok {
		c.Request = c.Request.WithContext(service.ContextWithSalesReferral(c.Request.Context(), referral))
	}
}

type salesOAuthReferral struct {
	State     string `json:"state"`
	Referral  string `json:"referral"`
	ExpiresAt int64  `json:"expires_at"`
}

// The promotion source is sealed together with OAuth state at authorization
// start. A different tab cannot replace it by changing the first-touch cookie
// while the user is at the identity provider.
func (h *AuthHandler) captureSalesOAuthReferral(c *gin.Context, provider, state string) {
	referral := service.SalesReferralFromContext(c.Request.Context())
	if referral == "" || h.cfg == nil || h.cfg.JWT.Secret == "" {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: "sales_oauth_" + provider, Value: encodeSalesOAuthReferral(h.cfg.JWT.Secret, salesOAuthReferral{State: state, Referral: referral, ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}),
		Path: "/api/v1/auth", MaxAge: 600, HttpOnly: true, Secure: isRequestHTTPS(c), SameSite: http.SameSiteLaxMode,
	})
}

func encodeSalesOAuthReferral(secret string, saved salesOAuthReferral) string {
	payload, _ := json.Marshal(saved)
	data := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte("sales-oauth-state-v1:"+secret))
	_, _ = mac.Write([]byte(data))
	return data + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *AuthHandler) restoreSalesOAuthReferral(c *gin.Context, provider, state string) {
	// Missing state means this flow had no captured source. Do not attach a
	// referral acquired only after authorization started.
	c.Request = c.Request.WithContext(service.ContextWithSalesReferral(c.Request.Context(), ""))
	name := "sales_oauth_" + provider
	value, err := c.Cookie(name)
	if err != nil {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Path: "/api/v1/auth", MaxAge: -1, HttpOnly: true, Secure: isRequestHTTPS(c), SameSite: http.SameSiteLaxMode})
	if h.cfg == nil || h.cfg.JWT.Secret == "" || len(value) > 3072 {
		return
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return
	}
	mac := hmac.New(sha256.New, []byte("sales-oauth-state-v1:"+h.cfg.JWT.Secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return
	}
	var saved salesOAuthReferral
	if json.Unmarshal(data, &saved) != nil || saved.State != state || saved.ExpiresAt <= time.Now().Unix() {
		return
	}
	c.Request = c.Request.WithContext(service.ContextWithSalesReferral(c.Request.Context(), saved.Referral))
}
