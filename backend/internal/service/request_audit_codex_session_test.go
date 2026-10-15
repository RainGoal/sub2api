package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditCodexHeaderSession(t *testing.T) {
	for _, tc := range []struct {
		name, explicit, id, resolution string
		values                         []string
	}{
		{name: "native request", values: []string{`{"session_id":"native-session","thread_id":"native-session","turn_id":"turn"}`}, id: "native-session", resolution: "resolved"},
		{name: "ordinary header wins", explicit: "ordinary", values: []string{`{"session_id":"native-session"}`}, id: "ordinary", resolution: "conflict"},
		{name: "invalid metadata preserves header", explicit: "ordinary", values: []string{`{`}, id: "ordinary", resolution: "resolved"},
		{name: "duplicate header", values: []string{`{"session_id":"first"}`, `{"session_id":"second"}`}, id: "first", resolution: "conflict"},
		{name: "invalid first header", values: []string{`{"session_id":123}`, `{"session_id":"second"}`}, resolution: "invalid"},
		{name: "duplicate JSON field", values: []string{`{"session_id":"first","session_id":"second"}`}, id: "first", resolution: "conflict"},
		{name: "invalid first JSON field", values: []string{`{"session_id":123,"session_id":"second"}`}, resolution: "invalid"},
		{name: "malformed JSON", values: []string{`{"session_id":"broken"`}, resolution: "invalid"},
		{name: "array", values: []string{`[{"session_id":"wrong"}]`}, resolution: "invalid"},
		{name: "object session", values: []string{`{"session_id":{"value":"wrong"}}`}, resolution: "invalid"},
		{name: "control", values: []string{`{"session_id":"bad\nvalue"}`}, resolution: "invalid"},
		{name: "invalid UTF8", values: []string{"{\"session_id\":\"bad\xffvalue\"}"}, resolution: "invalid"},
		{name: "oversize", values: []string{`{"session_id":"` + strings.Repeat("x", 256) + `"}`}, resolution: "invalid"},
		{name: "thread turn window and cache are not sessions", values: []string{`{"thread_id":"thread","turn_id":"turn","window_id":"window","prompt_cache_key":"cache"}`}, resolution: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header[http.CanonicalHeaderKey(openAIWSTurnMetadataHeader)] = tc.values
			if tc.explicit != "" {
				c.Request.Header.Set("session-id", tc.explicit)
			}
			original := c.Request.Header.Clone()
			id, source, resolution := ExtractRequestAuditSessionID(c)
			require.Equal(t, tc.id, id)
			require.Equal(t, tc.resolution, resolution)
			if tc.explicit != "" {
				require.Equal(t, "header.session-id", source)
			} else if tc.id != "" {
				require.Equal(t, "header.x-codex-turn-metadata.session_id", source)
			} else {
				require.Empty(t, source)
			}
			require.Equal(t, tc.explicit, ExtractClientSessionID(c), "usage session semantics must remain unchanged")
			require.Equal(t, original, c.Request.Header)
		})
	}
}

func TestRequestAuditCodexBodySession(t *testing.T) {
	for _, tc := range []struct {
		name, body, id, source string
		limit                  int64
	}{
		{name: "direct", body: `{"client_metadata":{"session_id":" direct "}}`, id: "direct", source: "body.client_metadata.session_id"},
		{name: "direct precedes embedded", body: `{"client_metadata":{"session_id":"direct","x-codex-turn-metadata":"{\"session_id\":\"embedded\"}"}}`, id: "direct", source: "body.client_metadata.session_id"},
		{name: "embedded", body: `{"client_metadata":{"x-codex-turn-metadata":"{\"session_id\":\"embedded\"}"}}`, id: "embedded", source: "body.client_metadata.x-codex-turn-metadata.session_id"},
		{name: "WS envelope", body: `{"type":"response.create","response":{"client_metadata":{"session_id":"nested"}}}`, id: "nested", source: "body.client_metadata.session_id"},
		{name: "non session IDs", body: `{"prompt_cache_key":"cache","client_metadata":{"thread_id":"thread","turn_id":"turn","window_id":"window","x-codex-turn-metadata":"{\"thread_id\":\"thread\"}"}}`},
		{name: "unrelated response object", body: `{"response":{"client_metadata":{"session_id":"wrong"}}}`},
		{name: "numeric session", body: `{"client_metadata":{"session_id":123}}`},
		{name: "object session", body: `{"client_metadata":{"session_id":{"session_id":"wrong"}}}`},
		{name: "embedded must be string", body: `{"client_metadata":{"x-codex-turn-metadata":{"session_id":"wrong"}}}`},
		{name: "oversize session", body: `{"client_metadata":{"session_id":"` + strings.Repeat("x", 256) + `"}}`},
		{name: "control session", body: `{"client_metadata":{"session_id":"bad\u0000id"}}`},
		{name: "invalid UTF8", body: "{\"client_metadata\":{\"session_id\":\"bad\xffid\"}}"},
		{name: "invalid JSON", body: `{"client_metadata":{"session_id":"bad"}`},
		{name: "valid body over limit", body: `{"client_metadata":{"session_id":"direct"}}`, limit: 8},
		{name: "normalization over limit", body: "{\"client_metadata\":{\"session_id\":\"direct\"},\"input\":\"\x00\x00\x00\x00\x00\"}", limit: 70},
		{name: "BOM and control copy", body: "\xef\xbb\xbf{\"client_metadata\":{\"session_id\":\"direct\"},\"input\":\"a\nb\"}", id: "direct", source: "body.client_metadata.session_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(tc.body)
			id, source, _ := CodexMetadataSessionID(payload, tc.limit)
			require.Equal(t, tc.id, id)
			require.Equal(t, tc.source, source)
			require.Equal(t, tc.body, string(payload), "audit parsing must not alter the input")
		})
	}
}
