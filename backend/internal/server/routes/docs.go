package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterDocsRoutes(v1 *gin.RouterGroup, h *handler.Handlers, limiter *middleware.PanelRateLimiter) {
	if h.Docs == nil {
		return
	}
	docs := v1.Group("/docs")
	docs.Use(limiter.PublicIP())
	docs.GET("/navigation", h.Docs.Navigation)
	docs.GET("/pages/:slug", h.Docs.Page)
	docs.GET("/search", h.Docs.Search)
}

func registerDocsAdminRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if h.Admin.Docs == nil {
		return
	}
	docs := admin.Group("/docs")
	docs.GET("/tree", h.Admin.Docs.Tree)
	docs.PUT("/tree", h.Admin.Docs.SaveTree)
	docs.POST("/nodes", h.Admin.Docs.Create)
	docs.PUT("/nodes/:id", h.Admin.Docs.Update)
	docs.DELETE("/nodes/:id", h.Admin.Docs.Delete)
	docs.GET("/pages/:id", h.Admin.Docs.Get)
	docs.PUT("/pages/:id/draft", h.Admin.Docs.SaveDraft)
	docs.POST("/pages/:id/publish", h.Admin.Docs.Publish)
	docs.POST("/pages/:id/unpublish", h.Admin.Docs.Unpublish)
}
