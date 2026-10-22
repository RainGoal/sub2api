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

func TestVideoModelTemplatesZhiqiUsesEmptyExplicitModelCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &AccountHandler{}
	router.GET("/api/v1/admin/accounts/video-model-templates", handler.GetVideoModelTemplates)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/accounts/video-model-templates?protocol=zhiqi_v1", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Data struct {
			Protocol string `json:"protocol"`
			Version  string `json:"version"`
			Preset   struct {
				BaseURL string `json:"base_url"`
			} `json:"preset"`
			Models []videoprovider.VideoModelCapability `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, string(videoprovider.ProviderZhiqiV1), result.Data.Protocol)
	require.Equal(t, "zhiqi-v1", result.Data.Version)
	require.Equal(t, videoprovider.DefaultZhiqiBaseURL, result.Data.Preset.BaseURL)
	require.NotNil(t, result.Data.Models)
	require.Empty(t, result.Data.Models, "supplier model names must come from discovery or explicit account configuration")
	for _, sensitive := range []string{"api_key", "credentials", "upstream_model", "bblabu", "Seedance-2.0"} {
		require.NotContains(t, recorder.Body.String(), sensitive)
	}
}

func TestVideoModelTemplatesRejectsUnknownOrLegacyProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &AccountHandler{}
	router.GET("/api/v1/admin/accounts/video-model-templates", handler.GetVideoModelTemplates)
	for _, protocol := range []string{"unknown-protocol", "bblabu_v1", "fflink_v1"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
			"/api/v1/admin/accounts/video-model-templates?protocol="+protocol, nil))
		require.Equal(t, http.StatusBadRequest, recorder.Code, protocol)
		require.NotContains(t, recorder.Body.String(), "api_key")
	}
}
