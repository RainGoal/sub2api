package handler

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SalesHandler struct{ sales *service.SalesService }

func NewSalesHandler(sales *service.SalesService) *SalesHandler { return &SalesHandler{sales: sales} }

// Referral receives only a registered promotion code. Nginx points partner
// domains at this endpoint on the canonical main origin with a fixed code.
func (h *SalesHandler) Referral(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	token, target, err := h.sales.IssueReferral(c.Request.Context(), c.Query("code"))
	if response.ErrorFrom(c, err) {
		return
	}
	u, err := url.Parse(target)
	if err != nil {
		response.ErrorFrom(c, service.ErrSalesInvalid)
		return
	}
	if !strings.EqualFold(c.Request.Host, u.Host) {
		c.Redirect(http.StatusFound, u.Scheme+"://"+u.Host+"/api/v1/sales/referral?code="+url.QueryEscape(c.Query("code")))
		return
	}
	if existing, cookieErr := c.Cookie(service.SalesReferralCookieName); cookieErr == nil && strings.Count(existing, ".") == 1 {
		attribution, resolveErr := h.sales.ResolveReferral(c.Request.Context(), existing)
		if resolveErr == nil && attribution != nil {
			// First valid source wins. Do not refresh its original expiry.
			c.Redirect(http.StatusFound, target)
			return
		}
		if resolveErr != nil && !service.IsSalesAttributionUnavailable(resolveErr) {
			response.ErrorFrom(c, resolveErr)
			return
		}
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: service.SalesReferralCookieName, Value: token, Path: "/", MaxAge: 30 * 24 * 60 * 60, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	c.Redirect(http.StatusFound, target)
}

func (h *SalesHandler) self(c *gin.Context) (*service.SalesPartner, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return nil, false
	}
	p, err := h.sales.SelfPartner(c.Request.Context(), subject.UserID)
	if response.ErrorFrom(c, err) {
		return nil, false
	}
	return p, true
}

func salesSelfFilter(c *gin.Context, partnerID int64) (service.SalesFilter, bool) {
	page, size := response.ParsePagination(c)
	f := service.SalesFilter{PartnerID: partnerID, Page: page, PageSize: size}
	for _, v := range []struct {
		key string
		dst **time.Time
		end bool
	}{{"start_date", &f.StartAt, false}, {"end_date", &f.EndAt, true}} {
		if raw := c.Query(v.key); raw != "" {
			t, err := time.Parse("2006-01-02", raw)
			if err != nil {
				response.BadRequest(c, "Invalid date filter")
				return f, false
			}
			if v.end {
				t = t.AddDate(0, 0, 1)
			}
			*v.dst = &t
		}
	}
	if f.StartAt != nil && f.EndAt != nil && !f.StartAt.Before(*f.EndAt) {
		response.BadRequest(c, "Invalid date range")
		return f, false
	}
	return service.NormalizeSalesFilter(f), true
}

func (h *SalesHandler) Me(c *gin.Context) {
	p, ok := h.self(c)
	if !ok {
		return
	}
	f, ok := salesSelfFilter(c, p.ID)
	if !ok {
		return
	}
	v, err := h.sales.Overview(c.Request.Context(), p.ID, f)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}

func (h *SalesHandler) Customers(c *gin.Context) {
	p, ok := h.self(c)
	if !ok {
		return
	}
	f, ok := salesSelfFilter(c, p.ID)
	if !ok {
		return
	}
	items, total, err := h.sales.ListCustomers(c.Request.Context(), f)
	if response.ErrorFrom(c, err) {
		return
	}
	for i := range items {
		items[i].Email = maskSalesEmail(items[i].Email)
	}
	response.Paginated(c, items, total, f.Page, f.PageSize)
}

func maskSalesEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	runes := []rune(parts[0])
	if len(runes) == 0 {
		return "***@" + parts[1]
	}
	return string(runes[0]) + "***@" + parts[1]
}

func (h *SalesHandler) Ledger(c *gin.Context) {
	p, ok := h.self(c)
	if !ok {
		return
	}
	f, ok := salesSelfFilter(c, p.ID)
	if !ok {
		return
	}
	items, total, err := h.sales.ListLedger(c.Request.Context(), f)
	if !response.ErrorFrom(c, err) {
		response.Paginated(c, items, total, f.Page, f.PageSize)
	}
}
func (h *SalesHandler) Settlements(c *gin.Context) {
	p, ok := h.self(c)
	if !ok {
		return
	}
	f, ok := salesSelfFilter(c, p.ID)
	if !ok {
		return
	}
	items, total, err := h.sales.ListSettlements(c.Request.Context(), f)
	if !response.ErrorFrom(c, err) {
		response.Paginated(c, items, total, f.Page, f.PageSize)
	}
}
