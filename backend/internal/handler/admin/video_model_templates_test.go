package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoModelTemplatesReturnsCapabilityContractWithoutCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &AccountHandler{}
	router.GET("/api/v1/admin/accounts/video-model-templates", handler.GetVideoModelTemplates)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/video-model-templates", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Data struct {
			Protocol string                               `json:"protocol"`
			Version  string                               `json:"version"`
			Models   []videoprovider.VideoModelCapability `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, string(videoprovider.ProviderVideoTasksJSONV1), result.Data.Protocol)
	require.Equal(t, "bblabu-2026-10-16.1", result.Data.Version)
	require.Len(t, result.Data.Models, 33)
	require.NoError(t, videoprovider.ValidateVideoModelCapabilities(result.Data.Models))
	require.NotContains(t, recorder.Body.String(), "api_key")
	for _, capability := range result.Data.Models {
		require.False(t, capability.SupportsCancel)
	}
}
