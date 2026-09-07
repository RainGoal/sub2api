package admin

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SalesHandler struct{ sales *service.SalesService }

func NewSalesHandler(sales *service.SalesService) *SalesHandler { return &SalesHandler{sales: sales} }

func salesAdminActor(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || role != "admin" {
		response.Forbidden(c, "Administrator access required")
		return 0, false
	}
	return subject.UserID, true
}
func salesAdminID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid id")
		return 0, false
	}
	return id, true
}
func salesAdminFilter(c *gin.Context) (service.SalesFilter, bool) {
	page, size := response.ParsePagination(c)
	f := service.SalesFilter{Page: page, PageSize: size, Search: c.Query("search")}
	if raw := c.Query("partner_id"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			response.BadRequest(c, "Invalid partner_id")
			return f, false
		}
		f.PartnerID = v
	}
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

func (h *SalesHandler) GetSettings(c *gin.Context) {
	if _, ok := salesAdminActor(c); !ok {
		return
	}
	v, err := h.sales.GetSettings(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *SalesHandler) SaveSettings(c *gin.Context) {
	actor, ok := salesAdminActor(c)
	if !ok {
		return
	}
	var in service.SalesSettings
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	if err := h.sales.SaveSettings(c.Request.Context(), &in, actor); !response.ErrorFrom(c, err) {
		response.Success(c, in)
	}
}
func (h *SalesHandler) ListPartners(c *gin.Context) {
	if _, ok := salesAdminActor(c); !ok {
		return
	}
	f, ok := salesAdminFilter(c)
	if !ok {
		return
	}
	v, total, err := h.sales.ListPartners(c.Request.Context(), f)
	if !response.ErrorFrom(c, err) {
		response.Paginated(c, v, total, f.Page, f.PageSize)
	}
}
func (h *SalesHandler) CreatePartner(c *gin.Context) { h.savePartner(c, 0) }
func (h *SalesHandler) UpdatePartner(c *gin.Context) {
	id, ok := salesAdminID(c)
	if !ok {
		return
	}
	h.savePartner(c, id)
}
func (h *SalesHandler) savePartner(c *gin.Context, id int64) {
	actor, ok := salesAdminActor(c)
	if !ok {
		return
	}
	var in service.SalesPartnerInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	v, err := h.sales.SavePartner(c.Request.Context(), id, in, actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *SalesHandler) Overview(c *gin.Context) {
	if _, ok := salesAdminActor(c); !ok {
		return
	}
	id, ok := salesAdminID(c)
	if !ok {
		return
	}
	f, ok := salesAdminFilter(c)
	if !ok {
		return
	}
	v, err := h.sales.Overview(c.Request.Context(), id, f)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *SalesHandler) Customers(c *gin.Context) {
	if _, ok := salesAdminActor(c); !ok {
		return
	}
	f, ok := salesAdminFilter(c)
	if !ok {
		return
	}
	v, total, err := h.sales.ListCustomers(c.Request.Context(), f)
	if !response.ErrorFrom(c, err) {
		response.Paginated(c, v, total, f.Page, f.PageSize)
	}
}
func (h *SalesHandler) Ledger(c *gin.Context) {
	if _, ok := salesAdminActor(c); !ok {
		return
	}
	f, ok := salesAdminFilter(c)
	if !ok {
		return
	}
	v, total, err := h.sales.ListLedger(c.Request.Context(), f)
	if !response.ErrorFrom(c, err) {
		response.Paginated(c, v, total, f.Page, f.PageSize)
	}
}
func (h *SalesHandler) Settlements(c *gin.Context) {
	if _, ok := salesAdminActor(c); !ok {
		return
	}
	f, ok := salesAdminFilter(c)
	if !ok {
		return
	}
	v, total, err := h.sales.ListSettlements(c.Request.Context(), f)
	if !response.ErrorFrom(c, err) {
		response.Paginated(c, v, total, f.Page, f.PageSize)
	}
}
func (h *SalesHandler) CreateSettlement(c *gin.Context) {
	actor, ok := salesAdminActor(c)
	if !ok {
		return
	}
	var in service.SalesSettlementInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	v, err := h.sales.CreateSettlement(c.Request.Context(), in, actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *SalesHandler) ConfirmSettlement(c *gin.Context) {
	actor, ok := salesAdminActor(c)
	if !ok {
		return
	}
	id, ok := salesAdminID(c)
	if !ok {
		return
	}
	var in struct {
		RequestKey string `json:"request_key"`
	}
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	v, err := h.sales.ConfirmSettlement(c.Request.Context(), id, in.RequestKey, actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *SalesHandler) PaySettlement(c *gin.Context) {
	actor, ok := salesAdminActor(c)
	if !ok {
		return
	}
	id, ok := salesAdminID(c)
	if !ok {
		return
	}
	var in service.SalesPaymentInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	v, err := h.sales.PaySettlement(c.Request.Context(), id, in, actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *SalesHandler) Adjust(c *gin.Context) {
	actor, ok := salesAdminActor(c)
	if !ok {
		return
	}
	var in service.SalesAdjustmentInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	v, err := h.sales.Adjust(c.Request.Context(), in, actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
