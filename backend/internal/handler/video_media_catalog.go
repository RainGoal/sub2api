package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) MediaModels(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	models, err := h.gatewayService.ListVideoMediaModels(c.Request.Context(), key)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "media_catalog_unavailable", "Media model catalog is unavailable")
		return
	}
	c.Header("Cache-Control", "no-store")
	catalogVersion, priceVersion := service.VideoMediaCatalogVersions(models)
	c.JSON(http.StatusOK, gin.H{"object": "list", "schema_version": 1, "catalog_version": catalogVersion, "price_version": priceVersion, "data": models})
}
