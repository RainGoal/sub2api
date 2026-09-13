package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CommunityLocalizedText keeps public community copy readable in both clients.
// Empty translations are permitted so clients can use their localized defaults.
type CommunityLocalizedText struct {
	ZhCN string `json:"zh-CN"`
	EnUS string `json:"en-US"`
}

// CommunityContactSettings is public, optional contact information. An empty
// group number, invitation and QR image disables the community entry.
type CommunityContactSettings struct {
	GroupName   CommunityLocalizedText `json:"group_name"`
	Description CommunityLocalizedText `json:"description"`
	GroupNumber string                 `json:"group_number"`
	InviteURL   string                 `json:"invite_url"`
	QRImageURL  string                 `json:"qr_image_url"`
}

func normalizeCommunityContact(value CommunityContactSettings) (CommunityContactSettings, error) {
	fields := []struct {
		name  string
		value *string
		limit int
	}{
		{"group_name.zh-CN", &value.GroupName.ZhCN, 120},
		{"group_name.en-US", &value.GroupName.EnUS, 120},
		{"description.zh-CN", &value.Description.ZhCN, 1000},
		{"description.en-US", &value.Description.EnUS, 1000},
		{"group_number", &value.GroupNumber, 32},
		{"invite_url", &value.InviteURL, 2048},
		{"qr_image_url", &value.QRImageURL, 2048},
	}
	for _, field := range fields {
		*field.value = strings.TrimSpace(*field.value)
		if utf8.RuneCountInString(*field.value) > field.limit {
			return CommunityContactSettings{}, fmt.Errorf("community_contact.%s must not exceed %d characters", field.name, field.limit)
		}
	}
	for _, digit := range value.GroupNumber {
		if digit < '0' || digit > '9' {
			return CommunityContactSettings{}, fmt.Errorf("community_contact.group_number must contain only digits")
		}
	}
	if !validCommunityContactURL(value.InviteURL, false) {
		return CommunityContactSettings{}, fmt.Errorf("community_contact.invite_url must be an absolute HTTP(S) URL without credentials")
	}
	if !validCommunityContactURL(value.QRImageURL, true) {
		return CommunityContactSettings{}, fmt.Errorf("community_contact.qr_image_url must be an HTTP(S) URL or a safe root-relative image path")
	}
	return value, nil
}

func validCommunityContactURL(raw string, allowPath bool) bool {
	if raw == "" {
		return true
	}
	if strings.Contains(raw, "\\") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return false
	}
	if allowPath && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return parsed.Scheme == "" && parsed.Host == ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	return (scheme == "http" || scheme == "https") && parsed.Hostname() != "" &&
		strings.HasPrefix(strings.ToLower(raw), scheme+"://")
}

func parseCommunityContact(raw string) CommunityContactSettings {
	var value CommunityContactSettings
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &value) != nil {
		return CommunityContactSettings{}
	}
	normalized, err := normalizeCommunityContact(value)
	if err != nil {
		return CommunityContactSettings{}
	}
	return normalized
}
