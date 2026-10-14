package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditSessionHeaderResolution(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		headers                http.Header
		id, source, resolution string
	}{
		{"missing", http.Header{"Prompt_cache_key": {"cache"}}, "", "", "missing"},
		{"invalid", http.Header{"Session-Id": {strings.Repeat("x", 256)}}, "", "", "invalid"},
		{"priority and conflict", http.Header{"Session-Id": {" first "}, "Session_id": {"second"}}, "first", "header.session-id", "conflict"},
		{"case preserved", http.Header{"Session_id": {"CaseSensitive"}}, "CaseSensitive", "header.session_id", "resolved"},
		{"duplicate conflict", http.Header{"Session-Id": {"first", "second"}}, "first", "header.session-id", "conflict"},
		{"invalid first value does not substitute second", http.Header{"Session-Id": {"bad\x00id", "second"}}, "", "", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Request.Header = tc.headers
			id, source, resolution := ExtractRequestAuditSessionID(c)
			require.Equal(t, tc.id, id)
			require.Equal(t, tc.source, source)
			require.Equal(t, tc.resolution, resolution)
			require.Equal(t, id, ExtractClientSessionID(c), "audit must preserve the usage header identity priority")
		})
	}
}

func TestRequestAuditClaudeMetadataSessionFormats(t *testing.T) {
	const session = "550e8400-e29b-41d4-a716-446655440000"
	for _, raw := range []string{
		`{"device_id":"device","session_id":"` + session + `"}`,
		"user_" + strings.Repeat("a", 64) + "_account__session_" + session,
	} {
		body, err := json.Marshal(map[string]any{"metadata": map[string]string{"user_id": raw}})
		require.NoError(t, err)
		require.Equal(t, session, ClaudeMetadataSessionID(body, 0))
	}
	require.Empty(t, ClaudeMetadataSessionID([]byte(`{"metadata":{"user_id":"{\"device_id\":\"a\",\"session_id\":\"b\"}"}`), 0))
	require.Empty(t, ClaudeMetadataSessionID([]byte(`{"prompt_cache_key":"cache","request_id":"request"}`), 0))
}

func TestRequestAuditClaudeMetadataAcceptsLenientJSONWithoutChangingOriginal(t *testing.T) {
	const body = `{"metadata":{"user_id":"{\"device_id\":\"device\",\"session_id\":\"banned-session\"}"},"input":"before%safter"}`
	for _, tc := range []struct {
		name, prefix, control string
	}{
		{name: "UTF-8 BOM", prefix: "\xef\xbb\xbf"},
		{name: "raw newline", control: "\n"},
		{name: "raw DEL", control: "\x7f"},
		{name: "BOM and raw NUL", prefix: "\xef\xbb\xbf", control: "\x00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.prefix + strings.Replace(body, "%s", tc.control, 1)
			payload := []byte(original)
			require.Equal(t, "banned-session", ClaudeMetadataSessionID(payload, 0))
			require.Equal(t, original, string(payload), "normalization must not rewrite captured input")
		})
	}
}
