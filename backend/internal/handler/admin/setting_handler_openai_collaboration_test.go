//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func readCollaborationSettingsResponse(t *testing.T, rec *httptest.ResponseRecorder) service.OpenAICollaborationSettings {
	t.Helper()
	var response struct {
		Data struct {
			Settings *service.OpenAICollaborationSettings `json:"openai_collaboration_settings"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotNil(t, response.Data.Settings)
	return *response.Data.Settings
}

func TestSettingsOpenAICollaborationRoundTripAndShutdown(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, nil)
	want := service.OpenAICollaborationSettings{Enabled: true, PlaintextEnabled: true,
		LegacyCipherRecoveryEnabled: true, BlankPreludeWaitSeconds: 10, GroupIDs: []int64{7}, APIKeyIDs: []int64{11}}
	rec := doUpdateSettings(t, h, map[string]any{"openai_collaboration_settings": want}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, want, readCollaborationSettingsResponse(t, rec))
	require.True(t, h.settingService.GetOpenAICollaborationSettingsCached(context.Background()).Allows(7, 11))
	stored, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, string(stored), repo.values[service.SettingKeyOpenAICollaborationSettings])
	rec = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, want, readCollaborationSettingsResponse(t, rec))
	want.Enabled = false
	rec = doUpdateSettings(t, h, map[string]any{"openai_collaboration_settings": want}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, h.settingService.GetOpenAICollaborationSettingsCached(context.Background()).Allows(7, 11))
}

func TestSettingsOpenAICollaborationOmittedOrNullPreservesValue(t *testing.T) {
	stored := `{"enabled":true,"plaintext_enabled":true,"blank_prelude_wait_seconds":10,"group_ids":[7],"api_key_ids":[]}`
	for _, body := range []map[string]any{{"site_name": "Updated"}, {"openai_collaboration_settings": nil}} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeyOpenAICollaborationSettings: stored})
		rec := doUpdateSettings(t, h, body, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, stored, repo.values[service.SettingKeyOpenAICollaborationSettings])
		require.True(t, readCollaborationSettingsResponse(t, rec).Enabled)
		require.Equal(t, 10, readCollaborationSettingsResponse(t, rec).BlankPreludeWaitSeconds)
	}
}

func TestSettingsOpenAICollaborationLegacySaveDefaultsBlankPrelude(t *testing.T) {
	for _, wait := range []any{nil, 0} {
		h, _ := newStepUpSwitchTestHandler(t, map[string]string{
			service.SettingKeyOpenAICollaborationSettings: `{"enabled":true,"group_ids":[7],"blank_prelude_wait_seconds":10}`,
		})
		settings := map[string]any{"enabled": true, "group_ids": []int64{7}}
		if wait != nil {
			settings["blank_prelude_wait_seconds"] = wait
		}
		rec := doUpdateSettings(t, h, map[string]any{"openai_collaboration_settings": settings}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, 2, readCollaborationSettingsResponse(t, rec).BlankPreludeWaitSeconds)
		require.Equal(t, 2, h.settingService.GetOpenAICollaborationSettingsCached(context.Background()).BlankPreludeWaitSeconds)
	}
}

func TestSettingsOpenAICollaborationRejectsInvalidBeforeWriting(t *testing.T) {
	for _, value := range []any{
		map[string]any{"enabled": true},
		map[string]any{"group_ids": []int64{7, 7}},
		map[string]any{"api_key_ids": []int64{-1}},
		map[string]any{"enabled": "true"},
		map[string]any{"blank_prelude_wait_seconds": -1},
		map[string]any{"blank_prelude_wait_seconds": 1},
		map[string]any{"blank_prelude_wait_seconds": 16},
		map[string]any{"blank_prelude_wait_seconds": 2.5},
		map[string]any{"blank_prelude_wait_seconds": "10"},
	} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeySiteName: "Existing"})
		rec := doUpdateSettings(t, h, map[string]any{"site_name": "Do not save", "openai_collaboration_settings": value}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Equal(t, "Existing", repo.values[service.SettingKeySiteName])
		require.Empty(t, repo.values[service.SettingKeyOpenAICollaborationSettings])
	}
}

func TestSettingsOpenAICollaborationChangeIsAudited(t *testing.T) {
	before := &service.SystemSettings{OpenAICollaborationSettings: service.DefaultOpenAICollaborationSettings()}
	after := &service.SystemSettings{OpenAICollaborationSettings: service.OpenAICollaborationSettings{Enabled: true, GroupIDs: []int64{7}}}
	require.Contains(t, diffSettings(before, after, nil, nil, UpdateSettingsRequest{}), service.SettingKeyOpenAICollaborationSettings)
	require.NotContains(t, diffSettings(after, after, nil, nil, UpdateSettingsRequest{}), service.SettingKeyOpenAICollaborationSettings)
	after = &service.SystemSettings{OpenAICollaborationSettings: before.OpenAICollaborationSettings}
	after.OpenAICollaborationSettings.BlankPreludeWaitSeconds = 10
	require.Contains(t, diffSettings(before, after, nil, nil, UpdateSettingsRequest{}), service.SettingKeyOpenAICollaborationSettings)
}
