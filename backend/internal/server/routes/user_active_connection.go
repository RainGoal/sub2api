package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func registerUserActiveConnectionRoutes(authenticated *gin.RouterGroup, active *service.ActiveConnectionService) {
	if authenticated == nil || active == nil {
		return
	}
	h := handler.NewActiveConnectionHandler(active)
	// Do not attach the usage Heavy() limiter: this endpoint is intentionally a
	// long-lived panel subscription and has no database query cost.
	activeGroup := authenticated.Group("/usage/active")
	activeGroup.GET("/events", h.Events)
}
