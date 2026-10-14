package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditHeaderPreviewRedactsURIQuery(t *testing.T) {
	for _, tc := range []struct {
		name, uri, unchanged string
		query                url.Values
		invalid              bool
	}{
		{name: "Google key", uri: "/v1beta/models/gemini:generateContent?key=private-key&alt=sse", query: url.Values{"key": {"[REDACTED]"}, "alt": {"sse"}}},
		{name: "absolute URL", uri: "https://gateway.example/v1/messages?api_key=private-key&model=claude", query: url.Values{"api_key": {"[REDACTED]"}, "model": {"claude"}}},
		{name: "duplicate keys including empty", uri: "/v1?key=&key=first-secret&key=second-secret&keep=one&keep=two", query: url.Values{"key": {"[REDACTED]", "[REDACTED]", "[REDACTED]"}, "keep": {"one", "two"}}},
		{name: "encoded key and values", uri: "/v1/models/a%2Fb?%61pi%5Fkey=private%26secret%2Bvalue&safe=a%2Fb+z", query: url.Values{"api_key": {"[REDACTED]"}, "safe": {"a/b z"}}},
		{name: "case insensitive", uri: "/v1?API_KEY=private-key&KeY=another-key", query: url.Values{"API_KEY": {"[REDACTED]"}, "KeY": {"[REDACTED]"}}},
		{name: "shared secret query policy", uri: "/v1?client_secret=client-private&access_token=token-private&apikey=key-private&token=short-private&secret=other-private&code=code-private", query: url.Values{"client_secret": {"[REDACTED]"}, "access_token": {"[REDACTED]"}, "apikey": {"[REDACTED]"}, "token": {"[REDACTED]"}, "secret": {"[REDACTED]"}, "code": {"[REDACTED]"}}},
		{name: "invalid path encoding", uri: "/%zz?key=private-key", invalid: true},
		{name: "invalid query encoding", uri: "/v1?key=private-key&bad=%zz", invalid: true},
		{name: "invalid query separator", uri: "/v1?key=private-key;alt=sse", invalid: true},
		{name: "invalid relative request target", uri: "relative?key=private-key", invalid: true},
		{name: "public query preserved exactly", uri: "/v1/models/a%2fb?z=first&z=second&model=a%2Fb+z&flag&empty=", unchanged: "/v1/models/a%2fb?z=first&z=second&model=a%2Fb+z&flag&empty="},
		{name: "double encoded noncredential key", uri: "/v1?%256Bey=public&api_key_hint=public", unchanged: "/v1?%256Bey=public&api_key_hint=public"},
		{name: "no query", uri: "/v1/models/a%2Fb", unchanged: "/v1/models/a%2Fb"},
		{name: "empty request target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := json.Marshal(map[string]any{"request_uri": tc.uri, "headers": map[string][]string{"X-Normal": {"one", "two"}}})
			require.NoError(t, err)
			snapshot := string(original)
			redacted, err := redactRequestAuditHeaders(original)
			require.NoError(t, err)
			require.Equal(t, snapshot, string(original))
			var envelope struct {
				RequestURI string              `json:"request_uri"`
				Headers    map[string][]string `json:"headers"`
			}
			require.NoError(t, json.Unmarshal(redacted, &envelope))
			require.Equal(t, []string{"one", "two"}, envelope.Headers["X-Normal"])
			if tc.invalid {
				require.Equal(t, "[REDACTED]", envelope.RequestURI)
				return
			}
			if tc.query == nil {
				require.Equal(t, tc.unchanged, envelope.RequestURI)
				return
			}
			before, err := url.ParseRequestURI(tc.uri)
			require.NoError(t, err)
			after, err := url.ParseRequestURI(envelope.RequestURI)
			require.NoError(t, err)
			require.Equal(t, before.Scheme, after.Scheme)
			require.Equal(t, before.Host, after.Host)
			require.Equal(t, before.EscapedPath(), after.EscapedPath())
			require.Equal(t, tc.query, after.Query())
		})
	}
}

func TestRequestAuditURIUserinfoRedaction(t *testing.T) {
	for _, raw := range []string{
		"https://private-user:private-password@gateway.example/v1/messages",
		"https://private-user:private-password@gateway.example/v1/messages?model=claude",
		"https://private-token@gateway.example/v1/messages?alt=sse",
		"https://private%3Auser:private%40password@gateway.example/v1/messages?key=private-key&key=other-key&alt=sse",
	} {
		t.Run(raw, func(t *testing.T) {
			redacted := redactRequestAuditURI(raw)
			require.NotContains(t, redacted, "private")
			before, err := url.ParseRequestURI(raw)
			require.NoError(t, err)
			after, err := url.ParseRequestURI(redacted)
			require.NoError(t, err)
			require.Equal(t, "[REDACTED]", after.User.Username())
			_, hasPassword := after.User.Password()
			require.False(t, hasPassword)
			require.Equal(t, before.Scheme, after.Scheme)
			require.Equal(t, before.Host, after.Host)
			require.Equal(t, before.EscapedPath(), after.EscapedPath())
			want := before.Query()
			for i := range want["key"] {
				want["key"][i] = "[REDACTED]"
			}
			require.Equal(t, want, after.Query())
		})
	}
}

func TestRequestAuditURIQueryRedactionOnlyAffectsDefaultHeaderPreview(t *testing.T) {
	const original = `{"request_uri":"https://private-user:private-password@gateway.example/v1beta/models/gemini:generateContent?key=private-key&alt=sse","headers":{"X-Normal":["value"]}}`
	data := []byte(original)
	h := &RequestAuditHandler{reads: make(chan struct{}, 2), store: requestAuditStoreStub{read: func(_ string, _ string, offset, limit int64, _ bool) (requestaudit.PayloadResult, error) {
		end := min(offset+limit, int64(len(data)))
		return requestaudit.PayloadResult{Bytes: data[offset:end], TotalBytes: int64(len(data)), HasMore: end < int64(len(data)), CaptureState: "complete"}, nil
	}}}
	for _, query := range []string{"?part=headers", "?part=headers&reveal_sensitive=true", "?part=headers&mode=raw", "?part=body"} {
		t.Run(query, func(t *testing.T) {
			w := requestAuditTestRequest(requestAuditTestRouter("admin", h.Payload), http.MethodGet, query, "")
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, original, string(data), "stored bytes must not be rewritten")
			if query == "?part=headers&mode=raw" {
				require.Equal(t, original, w.Body.String())
				return
			}
			var result struct {
				Data struct {
					Content  string `json:"content"`
					Redacted bool   `json:"redacted"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			if query == "?part=headers" {
				require.True(t, result.Data.Redacted)
				require.NotContains(t, result.Data.Content, "private-key")
				require.NotContains(t, result.Data.Content, "private-user")
				require.NotContains(t, result.Data.Content, "private-password")
			} else {
				require.False(t, result.Data.Redacted)
				require.Equal(t, original, result.Data.Content)
			}
		})
	}
}
