package admin

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type upstreamBalanceAdminService interface {
	List(context.Context) (*service.UpstreamBalanceList, error)
	Discover(context.Context) (*service.UpstreamBalanceDiscovery, error)
	SaveConfig(context.Context, *service.UpstreamBalanceConfig) (*service.UpstreamBalanceConfig, error)
	Refresh(context.Context, string) (*service.UpstreamBalanceItem, error)
}

// UpstreamBalanceHandler only exposes monitor state, never account credentials.
type UpstreamBalanceHandler struct {
	service upstreamBalanceAdminService
}

func NewUpstreamBalanceHandler(svc *service.UpstreamBalanceService) *UpstreamBalanceHandler {
	h := &UpstreamBalanceHandler{}
	if svc != nil {
		h.service = svc
	}
	return h
}

func (h *UpstreamBalanceHandler) available(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	if h == nil || h.service == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Upstream balance monitoring is unavailable", "UPSTREAM_BALANCE_UNAVAILABLE", nil)
		return false
	}
	return true
}

func (h *UpstreamBalanceHandler) List(c *gin.Context) {
	if !h.available(c) {
		return
	}
	result, err := h.service.List(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *UpstreamBalanceHandler) Discover(c *gin.Context) {
	if !h.available(c) {
		return
	}
	result, err := h.service.Discover(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *UpstreamBalanceHandler) SaveConfig(c *gin.Context) {
	if !h.available(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var config service.UpstreamBalanceConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		response.BadRequest(c, "Invalid upstream balance configuration")
		return
	}
	result, err := h.service.SaveConfig(c.Request.Context(), &config)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *UpstreamBalanceHandler) Refresh(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		response.BadRequest(c, "Invalid wallet ID")
		return
	}
	result, err := h.service.Refresh(c.Request.Context(), id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}
