package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// PreviewSeedancePricingImport reads legacy prices without changing groups or
// channels. The ordinary channel save is the only write after review.
func (h *ChannelHandler) PreviewSeedancePricingImport(c *gin.Context) {
	var req struct {
		GroupIDs []int64 `json:"group_ids" binding:"required,min=1,max=1000,dive,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid group selection")
		return
	}
	prices, err := h.channelService.PreviewSeedanceSalesImport(c.Request.Context(), req.GroupIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	rows := make([]channelModelPricingResponse, 0, len(prices))
	for i := range prices {
		rows = append(rows, pricingToResponse(&prices[i]))
	}
	response.Success(c, gin.H{"model_pricing": rows})
}
