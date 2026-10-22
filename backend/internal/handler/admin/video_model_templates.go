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
	protocol := c.DefaultQuery("protocol", string(videoprovider.ProviderVideoTasksJSONV1))
	id, err := videoprovider.NormalizeID(protocol)
	if err != nil || !videoprovider.SupportsModelCapabilities(id) {
		response.BadRequest(c, "Unsupported video capability protocol")
		return
	}
	if id == videoprovider.ProviderZhiqiV1 {
		response.Success(c, gin.H{
			"protocol": id,
			"version":  "zhiqi-v1",
			"preset":   gin.H{"name": "Zhiqi", "base_url": videoprovider.DefaultZhiqiBaseURL},
			"models":   []videoprovider.VideoModelCapability{},
		})
		return
	}
	response.Success(c, gin.H{
		"protocol": videoprovider.ProviderVideoTasksJSONV1,
		"version":  "bblabu-2026-10-16.1",
		"preset":   gin.H{"name": "BBLabu", "base_url": videoprovider.DefaultBBLabuBaseURL},
		"models":   videoprovider.BBLabuModelCapabilities(),
	})
}
