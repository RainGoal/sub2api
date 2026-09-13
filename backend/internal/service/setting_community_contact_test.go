//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCommunityContactNormalizesOptionalLocalizedFields(t *testing.T) {
	value, err := normalizeCommunityContact(CommunityContactSettings{
		GroupName:   CommunityLocalizedText{ZhCN: " 社群 ", EnUS: " "},
		Description: CommunityLocalizedText{ZhCN: "\n使用交流\n"},
		GroupNumber: " 10000001 ",
		InviteURL:   " https://community.example/invite ",
		QRImageURL:  " /community/qr.png?v=1 ",
	})
	require.NoError(t, err)
	require.Equal(t, "社群", value.GroupName.ZhCN)
	require.Empty(t, value.GroupName.EnUS)
	require.Equal(t, "使用交流", value.Description.ZhCN)
	require.Equal(t, "10000001", value.GroupNumber)
	require.Equal(t, "https://community.example/invite", value.InviteURL)
	require.Equal(t, "/community/qr.png?v=1", value.QRImageURL)
	_, err = normalizeCommunityContact(CommunityContactSettings{})
	require.NoError(t, err)
}

func TestCommunityContactAcceptsSafeURLs(t *testing.T) {
	for _, address := range []string{"", "http://community.example/invite", "https://community.example/invite?key=one#group", "HTTPS://community.example/invite"} {
		t.Run(address, func(t *testing.T) {
			_, err := normalizeCommunityContact(CommunityContactSettings{InviteURL: address, QRImageURL: address})
			require.NoError(t, err)
		})
	}
	_, err := normalizeCommunityContact(CommunityContactSettings{QRImageURL: "/community/qr.png"})
	require.NoError(t, err)
}

func TestCommunityContactRejectsUnsafeURLs(t *testing.T) {
	for _, address := range []string{
		"javascript:alert(1)", "data:image/png;base64,test", "file:///qr.png",
		"//outside.example/qr.png", "/\\outside.example/qr.png", "qr.png",
		"https:outside.example", "https://name:pass@outside.example/qr.png",
		"https://outside.example\\qr.png", "https://out\nside.example/qr.png",
		"https:///qr.png", "https://outside.example/%zz",
	} {
		t.Run(address, func(t *testing.T) {
			_, err := normalizeCommunityContact(CommunityContactSettings{InviteURL: address})
			require.Error(t, err)
			_, err = normalizeCommunityContact(CommunityContactSettings{QRImageURL: address})
			require.Error(t, err)
		})
	}
	_, err := normalizeCommunityContact(CommunityContactSettings{InviteURL: "/community/invite"})
	require.Error(t, err)
}

func TestCommunityContactRejectsInvalidNumbersAndOversizedCopy(t *testing.T) {
	for _, value := range []CommunityContactSettings{
		{GroupNumber: "1000abc"}, {GroupNumber: "１２３４５"}, {GroupNumber: strings.Repeat("1", 33)},
		{GroupName: CommunityLocalizedText{ZhCN: strings.Repeat("群", 121)}},
		{GroupName: CommunityLocalizedText{EnUS: strings.Repeat("n", 121)}},
		{Description: CommunityLocalizedText{ZhCN: strings.Repeat("文", 1001)}},
		{Description: CommunityLocalizedText{EnUS: strings.Repeat("d", 1001)}},
		{InviteURL: "https://community.example/" + strings.Repeat("a", 2048)},
		{QRImageURL: "/" + strings.Repeat("a", 2048)},
	} {
		_, err := normalizeCommunityContact(value)
		require.Error(t, err)
	}
	_, err := normalizeCommunityContact(CommunityContactSettings{GroupName: CommunityLocalizedText{ZhCN: strings.Repeat("😀", 120)}})
	require.NoError(t, err, "limits count Unicode characters rather than UTF-8 bytes")
}

func TestCommunityContactDefaultsFailClosedForMalformedStoredConfiguration(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", "[]", "broken", `{"invite_url":"javascript:alert(1)"}`, `{"group_name":"wrong type"}`} {
		require.Equal(t, CommunityContactSettings{}, parseCommunityContact(raw))
	}
}

func TestCommunityContactPublicAdminAndInjectionReadTheSameStoredObject(t *testing.T) {
	want := CommunityContactSettings{
		GroupName:   CommunityLocalizedText{ZhCN: "测试交流群", EnUS: "Test community"},
		Description: CommunityLocalizedText{ZhCN: "使用交流", EnUS: "Community discussion"},
		GroupNumber: "10000001", InviteURL: "https://community.example/invite", QRImageURL: "/community/qr.png",
	}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	values := map[string]string{SettingKeyCommunityContact: string(raw), SettingKeyContactInfo: "Existing support contact"}
	publicService := NewSettingService(&settingPublicRepoStub{values: values}, &config.Config{})
	public, err := publicService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, public.CommunityContact)
	require.Equal(t, values[SettingKeyContactInfo], public.ContactInfo)
	injection, err := publicService.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	payload, ok := injection.(*PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.Equal(t, want, payload.CommunityContact)
	adminService := NewSettingService(&settingGetAllRepoStub{values: values}, &config.Config{})
	admin, err := adminService.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, admin.CommunityContact)
	delete(values, SettingKeyCommunityContact)
	public, err = publicService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, CommunityContactSettings{}, public.CommunityContact)
}
