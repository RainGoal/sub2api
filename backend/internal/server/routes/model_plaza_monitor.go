package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// Uses the existing admin authentication, audit, compliance and scoped-key guards.
// This feature deliberately has its own enable switch, independent of monitor v1/v2.
func registerModelPlazaMonitorRoutes(admin *gin.RouterGroup, h *handler.Handlers, limiter *middleware.PanelRateLimiter) {
	if h.ModelPlazaMonitor == nil {
		return
	}
	monitor := admin.Group("/model-plaza-monitor")
	monitor.Use(limiter.Heavy())
	monitor.GET("/config", h.ModelPlazaMonitor.GetConfig)
	monitor.PUT("/config", h.ModelPlazaMonitor.UpdateConfig)
	monitor.GET("/candidates", h.ModelPlazaMonitor.Candidates)
	monitor.GET("/status", h.ModelPlazaMonitor.AdminStatus)
	monitor.POST("/run", h.ModelPlazaMonitor.Run)
}
