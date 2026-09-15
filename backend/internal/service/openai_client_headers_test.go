package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIClientHeadersBlockConfiguredModelDiagnostics(t *testing.T) {
	diagnostics := []string{"openai-model", "X-OpenAI-Model", "x-Upstream-model", "X-Response-Model", "x-model-id", "x-litellm-model-id"}
	filter := responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
		Enabled: true,
		AdditionalAllowed: append(append([]string{}, diagnostics...),
			"x-codex-turn-state", "x-codex-primary-reset-after-seconds", "x-provider-processing-ms"),
	})
	source := http.Header{
		"X-Request-Id":                        {"req_original"},
		"Retry-After":                         {"13"},
		"X-Ratelimit-Reset-Tokens":            {"3s"},
		"X-Ratelimit-Remaining-Requests":      {"7"},
		"X-Codex-Turn-State":                  {"opaque-state"},
		"X-Codex-Primary-Reset-After-Seconds": {"1800"},
		"X-Reasoning-Included":                {"true"},
		"X-Provider-Processing-Ms":            {"321"},
	}
	for _, key := range diagnostics {
		source[key] = []string{"internal-C"}
	}
	original := source.Clone()
	destination := http.Header{"x-MODEL": {"old-internal-C"}, "content-Length": {"9999"}}
	writeOpenAIClientResponseHeaders(destination, source, filter)
	require.Equal(t, original, source, "administrator and scheduling headers remain unchanged")
	for key, values := range destination {
		require.False(t, isOpenAIClientModelDiagnosticHeader(key), key)
		require.NotContains(t, values, "internal-C", key)
	}
	require.Empty(t, destination.Get("Content-Length"))
	for _, key := range []string{"X-Request-Id", "Retry-After", "X-Ratelimit-Reset-Tokens", "X-Ratelimit-Remaining-Requests", "X-Codex-Turn-State", "X-Codex-Primary-Reset-After-Seconds", "X-Reasoning-Included", "X-Provider-Processing-Ms"} {
		require.Equal(t, original.Values(key), destination.Values(key), key)
	}
}

func TestOpenAIClientHeadersRetainDefaultFilterSemantics(t *testing.T) {
	source := http.Header{
		"Retry-After":          {"Wed, 21 Oct 2026 07:28:00 GMT"},
		"X-Request-Id":         {"req_1", "req_2"},
		"Content-Length":       {"123"},
		"X-Unknown-Debug":      {"internal-C"},
		"X-Reasoning-Included": {"true"},
	}
	got := filterOpenAIClientResponseHeaders(source, nil)
	require.Equal(t, source.Values("X-Request-Id"), got.Values("X-Request-Id"))
	require.Equal(t, source.Get("Retry-After"), got.Get("Retry-After"))
	require.Equal(t, "true", got.Get("X-Reasoning-Included"))
	require.Empty(t, got.Get("Content-Length"))
	require.Empty(t, got.Get("X-Unknown-Debug"))
	writeOpenAIClientResponseHeaders(nil, source, nil)
	removeOpenAIClientDiagnosticHeaders(nil)
}

func TestGrokMediaResponseHeadersRetainConfiguredDiagnostics(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"data":[{"url":"https://example.com/image.png"}]}`)
	headers := http.Header{
		"X-Upstream-Model": {"grok-image-provider"},
		"X-Request-Id":     {"req_media"},
	}
	original := headers.Clone()
	filter := responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
		Enabled: true, AdditionalAllowed: []string{"x-upstream-model"},
	})
	writeGrokMediaResponse(c, &http.Response{StatusCode: http.StatusOK, Header: headers}, body, filter)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "grok-image-provider", rec.Header().Get("X-Upstream-Model"))
	require.Equal(t, "req_media", rec.Header().Get("X-Request-Id"))
	require.Equal(t, body, rec.Body.Bytes())
	require.Equal(t, original, headers)
}

func TestOpenAIPassthroughResponseHeadersPrivacyScope(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		for _, route := range []string{"json", "stream", "sse_to_json"} {
			t.Run(platform+"/"+route, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				svc := &OpenAIGatewayService{cfg: &config.Config{}, responseHeaderFilter: responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
					Enabled: true, AdditionalAllowed: []string{"x-upstream-model"},
				})}
				body := `{"id":"resp_headers","object":"response","model":"provider-model","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1}}`
				contentType := "application/json"
				if route != "json" {
					contentType = "text/event-stream"
					body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\ndata: [DONE]\n\n"
				}
				headers := http.Header{
					"Content-Type":     {contentType},
					"X-Upstream-Model": {"provider-model"},
					"X-Request-Id":     {"req_headers"},
				}
				original := headers.Clone()
				resp := &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
				account := &Account{ID: 1, Platform: platform, Type: AccountTypeAPIKey}
				var err error
				if route == "stream" {
					_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "public-model", "provider-model")
				} else {
					_, err = svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, "public-model", "provider-model")
				}
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, rec.Code)
				if platform == PlatformOpenAI {
					require.Empty(t, rec.Header().Get("X-Upstream-Model"))
				} else {
					require.Equal(t, "provider-model", rec.Header().Get("X-Upstream-Model"))
				}
				require.Equal(t, "req_headers", rec.Header().Get("X-Request-Id"))
				require.Equal(t, original, headers)
			})
		}
	}
}
