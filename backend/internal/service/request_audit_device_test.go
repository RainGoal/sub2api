package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestAuditHeaderDevice(t *testing.T) {
	for _, tc := range []struct {
		name, flat, metadata, want, resolution string
	}{
		{"missing", "", "", "", "missing"},
		{"flat exact case", "Device-ABC", "", "Device-ABC", "resolved"},
		{"embedded", "", `{"installation_id":"install-1","session_id":"session"}`, "install-1", "resolved"},
		{"header priority", "first", `{"installation_id":"second"}`, "first", "conflict"},
		{"duplicate JSON", "", `{"installation_id":"first","installation_id":"second"}`, "first", "conflict"},
		{"numeric", "", `{"installation_id":12}`, "", "invalid"},
		{"no session substitution", "", `{"session_id":"session","thread_id":"thread"}`, "", "missing"},
		{"invalid flat fallback", " bad ", `{"installation_id":"valid"}`, "valid", "resolved"},
		{"oversized", strings.Repeat("x", 256), "", "", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := make(http.Header)
			headers.Set("X-Codex-Installation-Id", tc.flat)
			headers.Set("X-Codex-Turn-Metadata", tc.metadata)
			deviceType, id, source, resolution := RequestAuditHeaderDevice(headers)
			require.Equal(t, tc.want, id)
			require.Equal(t, tc.resolution, resolution)
			if tc.want != "" {
				require.Equal(t, "codex", deviceType)
				require.Contains(t, source, "header.")
			}
			require.Equal(t, tc.metadata, headers.Get("X-Codex-Turn-Metadata"))
		})
	}
	_, id, _, resolution := RequestAuditHeaderDevice(http.Header{"X-Codex-Installation-Id": {"first", "second"}})
	require.Equal(t, "first", id)
	require.Equal(t, "conflict", resolution)
}

func TestRequestAuditBodyDevice(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantType, wantID string
		claude                       bool
	}{
		{"Claude JSON without session", `{"metadata":{"user_id":"{\"device_id\":\"Claude-ABC\"}"}}`, "claude", "Claude-ABC", true},
		{"Claude legacy", `{"metadata":{"user_id":"user_` + strings.Repeat("A", 64) + `_account__session_aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}}`, "claude", strings.Repeat("A", 64), true},
		{"Codex body", `{"client_metadata":{"x-codex-installation-id":"Codex-ABC"}}`, "codex", "Codex-ABC", false},
		{"Codex embedded", `{"client_metadata":{"x-codex-turn-metadata":"{\"installation_id\":\"install\"}"}}`, "codex", "install", false},
		{"WS envelope", `{"type":"response.create","response":{"client_metadata":{"x-codex-installation-id":"install"}}}`, "codex", "install", false},
		{"no inferred device", `{"prompt_cache_key":"cache","client_metadata":{"session_id":"session","thread_id":"thread"}}`, "", "", false},
		{"wrong protocol", `{"metadata":{"user_id":"{\"device_id\":\"device\"}"}}`, "", "", false},
		{"numeric", `{"client_metadata":{"x-codex-installation-id":123}}`, "", "", false},
		{"control", `{"metadata":{"user_id":"{\"device_id\":\"bad\\nID\"}"}}`, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			deviceType, id, _, _ := RequestAuditBodyDevice(body, tc.claude, 4096)
			require.Equal(t, tc.wantType, deviceType)
			require.Equal(t, tc.wantID, id)
			require.Equal(t, tc.body, string(body))
		})
	}
}

func TestRequestAuditBodyDeviceNormalizationAndBounds(t *testing.T) {
	const original = `{"client_metadata":{"x-codex-installation-id":"original"}}`
	for _, prefix := range []string{"", "\xef\xbb\xbf"} {
		body := []byte(prefix + original)
		_, id, _, _ := RequestAuditBodyDevice(body, false, 4096)
		require.Equal(t, "original", id)
		require.Equal(t, prefix+original, string(body))
		_, id, _, _ = RequestAuditBodyDevice(body, false, 8)
		require.Empty(t, id)
	}
	_, id, _, resolution := RequestAuditBodyDevice([]byte(`{"client_metadata":{"x-codex-installation-id":"first","x-codex-turn-metadata":"{\"installation_id\":\"second\"}"}}`), false, 4096)
	require.Equal(t, "first", id)
	require.Equal(t, "conflict", resolution)
}
