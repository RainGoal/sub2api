package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/gin-gonic/gin"
)

// GetVideoModelTemplates returns capability presets only. No upstream call is
// made and no discovered model is authorized or priced by this read operation.
// GET /api/v1/admin/accounts/video-model-templates
func (h *AccountHandler) GetVideoModelTemplates(c *gin.Context) {
	response.Success(c, gin.H{
		"protocol": videoprovider.ProviderVideoTasksJSONV1,
		"version":  "bblabu-2026-10-16.1",
		"preset":   gin.H{"name": "BBLabu", "base_url": videoprovider.DefaultBBLabuBaseURL},
		"models":   videoprovider.BBLabuModelCapabilities(),
	})
}
