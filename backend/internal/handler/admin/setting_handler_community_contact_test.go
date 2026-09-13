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

func readCommunityContactResponse(t *testing.T, rec *httptest.ResponseRecorder) service.CommunityContactSettings {
	t.Helper()
	var response struct {
		Data struct {
			CommunityContact *service.CommunityContactSettings `json:"community_contact"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotNil(t, response.Data.CommunityContact)
	return *response.Data.CommunityContact
}

func TestSettingsCommunityContactRoundTripAndCacheInvalidation(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeyContactInfo: "Existing support"})
	want := service.CommunityContactSettings{
		GroupName:   service.CommunityLocalizedText{ZhCN: "测试交流群", EnUS: "Test community"},
		Description: service.CommunityLocalizedText{ZhCN: "使用交流", EnUS: "Community discussion"},
		GroupNumber: "10000001", InviteURL: "https://community.example/invite", QRImageURL: "/community/qr.png",
	}
	invalidated := false
	h.settingService.SetOnUpdateCallback(func() { invalidated = true })
	rec := doUpdateSettings(t, h, map[string]any{"community_contact": want}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, want, readCommunityContactResponse(t, rec))
	require.True(t, invalidated, "saving must invalidate the existing settings/HTML cache")
	require.Equal(t, "Existing support", repo.values[service.SettingKeyContactInfo])
	stored, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, string(stored), repo.values[service.SettingKeyCommunityContact])
	public, err := h.settingService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, public.CommunityContact)
	rec = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, want, readCommunityContactResponse(t, rec))
}

func TestSettingsCommunityContactOmittedOrNullPreservesStoredValue(t *testing.T) {
	stored := `{ "group_number": "10000001", "invite_url": "https://community.example/invite" }`
	for _, body := range []map[string]any{{"site_name": "Updated site"}, {"community_contact": nil}} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeyCommunityContact: stored})
		rec := doUpdateSettings(t, h, body, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, stored, repo.values[service.SettingKeyCommunityContact], "omitted configuration must not be rewritten")
		require.Equal(t, "10000001", readCommunityContactResponse(t, rec).GroupNumber)
	}
}

func TestSettingsCommunityContactExplicitEmptyObjectClearsConfiguration(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeyCommunityContact: `{"group_number":"10000001"}`})
	rec := doUpdateSettings(t, h, map[string]any{"community_contact": map[string]any{}}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, service.CommunityContactSettings{}, readCommunityContactResponse(t, rec))
	var stored service.CommunityContactSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[service.SettingKeyCommunityContact]), &stored))
	require.Equal(t, service.CommunityContactSettings{}, stored)
	public, err := h.settingService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, service.CommunityContactSettings{}, public.CommunityContact)
}

func TestSettingsCommunityContactRejectsInvalidConfigurationBeforeWriting(t *testing.T) {
	for _, value := range []service.CommunityContactSettings{
		{GroupNumber: "not-a-number"}, {InviteURL: "javascript:alert(1)"},
		{InviteURL: "https://name:pass@outside.example/invite"}, {QRImageURL: "//outside.example/qr.png"},
		{QRImageURL: "/\\outside.example/qr.png"},
	} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{
			service.SettingKeySiteName: "Existing site", service.SettingKeyCommunityContact: `{"group_number":"10000001"}`,
		})
		invalidated := false
		h.settingService.SetOnUpdateCallback(func() { invalidated = true })
		rec := doUpdateSettings(t, h, map[string]any{"site_name": "Do not save", "community_contact": value}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), "INVALID_COMMUNITY_CONTACT")
		require.Equal(t, "Existing site", repo.values[service.SettingKeySiteName])
		require.Equal(t, `{"group_number":"10000001"}`, repo.values[service.SettingKeyCommunityContact])
		require.False(t, invalidated)
	}
}

func TestSettingsCommunityContactChangeIsAudited(t *testing.T) {
	before := &service.SystemSettings{}
	after := &service.SystemSettings{CommunityContact: service.CommunityContactSettings{GroupNumber: "10000001"}}
	require.Contains(t, diffSettings(before, after, nil, nil, UpdateSettingsRequest{}), "community_contact")
	require.NotContains(t, diffSettings(after, after, nil, nil, UpdateSettingsRequest{}), "community_contact")
}

func TestSettingsCommunityContactRejectsWrongJSONTypes(t *testing.T) {
	for _, value := range []any{"text", []string{}, map[string]any{"group_name": "text"}, map[string]any{"group_number": 123}} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeyCommunityContact: `{"group_number":"10000001"}`})
		rec := doUpdateSettings(t, h, map[string]any{"community_contact": value}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, `{"group_number":"10000001"}`, repo.values[service.SettingKeyCommunityContact])
	}
}
