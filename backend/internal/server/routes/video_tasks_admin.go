package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerVideoTaskReviewRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if h == nil || h.OpenAIGateway == nil {
		return
	}
	reviews := admin.Group("/video-tasks/reviews")
	reviews.GET("", h.OpenAIGateway.ListVideoTaskReviews)
	reviews.POST("/:state_id/resolve", h.OpenAIGateway.ResolveVideoTaskReview)
}
