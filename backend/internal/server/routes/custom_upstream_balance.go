package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// This group inherits administrator authentication, audit and compliance guards.
func registerUpstreamBalanceRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	balances := admin.Group("/upstream-balances")
	balances.GET("", h.Admin.UpstreamBalance.List)
	balances.GET("/discovery", h.Admin.UpstreamBalance.Discover)
	balances.PUT("/config", h.Admin.UpstreamBalance.SaveConfig)
	balances.POST("/:id/refresh", h.Admin.UpstreamBalance.Refresh)
}
