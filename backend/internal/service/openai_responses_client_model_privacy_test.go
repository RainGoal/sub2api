package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesClientModelPrivacy(t *testing.T) {
	for _, route := range []string{"json", "compact", "stream", "sse_to_json", "passthrough_json", "passthrough_stream", "passthrough_sse_to_json"} {
		for _, scenario := range []struct{ name, original, sent, declared string }{
			{"mapped", "public-A", "sent-B", "sent-B"},
			{"mismatch", "public-A", "sent-B", "declared-C"},
			{"unmapped", "public-A", "public-A", "declared-C"},
			{"composite", "channel-B", "sent-B", "declared-C"},
		} {
			t.Run(route+"/"+scenario.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				path := "/v1/responses"
				if route == "compact" {
					path += "/compact"
				}
				c.Request = httptest.NewRequest(http.MethodPost, path, nil)
				SetOpenAIClientRequestedModel(c, "public-A")
				cfg := &config.Config{}
				svc := &OpenAIGatewayService{cfg: cfg, responseHeaderFilter: responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
					Enabled: true, AdditionalAllowed: []string{"openai-model", "x-upstream-model"},
				})}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				body := fmt.Sprintf(`{"id":"resp_privacy","object":"response","model":%q,"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer names declared-C"}]}],"usage":{"input_tokens":19,"output_tokens":7,"input_tokens_details":{"cached_tokens":3}}}`, scenario.declared)
				contentType := "application/json"
				if strings.Contains(route, "stream") || strings.Contains(route, "sse_to_json") {
					contentType = "text/event-stream"
					body = fmt.Sprintf(": ping\n\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_privacy\",\"model\":%q}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"answer names declared-C\"}\n\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\ndata: [DONE]\n\n", scenario.declared, body)
				}
				resp := &http.Response{StatusCode: 200, Header: http.Header{
					"Content-Type": {contentType}, "Openai-Model": {scenario.declared}, "X-Upstream-Model": {scenario.sent}, "X-Request-Id": {"rid_privacy"},
				}, Body: io.NopCloser(iotest.OneByteReader(strings.NewReader(body)))}
				var usage *OpenAIUsage
				var err error
				switch route {
				case "stream":
					var result *openaiStreamingResult
					result, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), scenario.original, scenario.sent)
					if result != nil {
						usage = result.usage
					}
				case "passthrough_stream":
					var result *openaiStreamingResultPassthrough
					result, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), scenario.original, scenario.sent)
					if result != nil {
						usage = result.usage
					}
				default:
					if strings.HasPrefix(route, "passthrough") {
						var result *openaiNonStreamingResultPassthrough
						result, err = svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, scenario.original, scenario.sent)
						if result != nil {
							usage = result.usage
						}
					} else {
						var result *openaiNonStreamingResult
						result, err = svc.handleNonStreamingResponse(c.Request.Context(), resp, c, account, scenario.original, scenario.sent)
						if result != nil {
							usage = result.usage
						}
					}
				}
				require.NoError(t, err)
				require.NotNil(t, usage)
				require.Equal(t, OpenAIUsage{InputTokens: 19, OutputTokens: 7, CacheReadInputTokens: 3}, *usage)
				require.Equal(t, scenario.declared, observedUpstreamResponseModel(c))
				require.False(t, observedUpstreamResponseModelConflict(c))
				require.Equal(t, scenario.sent != scenario.declared, *upstreamModelMismatch(scenario.sent, observedUpstreamResponseModel(c)))
				require.Empty(t, rec.Header().Get("Openai-Model"))
				require.Empty(t, rec.Header().Get("X-Upstream-Model"))
				require.Contains(t, rec.Body.String(), "answer names declared-C")
				assertModel := func(payload []byte) {
					for _, field := range []string{"model", "response.model"} {
						if value := gjson.GetBytes(payload, field); value.Exists() {
							require.Equal(t, "public-A", value.String())
						}
					}
				}
				if route == "stream" || route == "passthrough_stream" {
					forEachOpenAISSEFrame(rec.Body.String(), func(_ string, payload []byte) { assertModel(payload) })
				} else {
					assertModel(rec.Body.Bytes())
				}
			})
		}
	}
}

func TestOpenAIResponsesClientPrivacyFailurePreservesPartialUsage(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientRequestedModel(c, "public-A")
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"private-C\"}}\n\n" +
					"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
					"data: {\"type\":\"response.usage\",\"usage\":{\"input_tokens\":11,\"output_tokens\":3}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"private-C\"}\n\n"))}
			var usage *OpenAIUsage
			var err error
			if passthrough {
				result, resultErr := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "public-A", "sent-B")
				require.NotNil(t, result)
				usage, err = result.usage, resultErr
			} else {
				result, resultErr := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "public-A", "sent-B")
				require.NotNil(t, result)
				usage, err = result.usage, resultErr
			}
			require.ErrorIs(t, err, errOpenAIClientPayload)
			var failover *UpstreamFailoverError
			require.NotErrorAs(t, err, &failover)
			require.NotNil(t, usage)
			require.Equal(t, 11, usage.InputTokens)
			require.Equal(t, 3, usage.OutputTokens)
			require.Equal(t, "private-C", observedUpstreamResponseModel(c))
			require.NotContains(t, rec.Body.String(), "private-C")
			require.Contains(t, rec.Body.String(), "response.failed")
			require.Contains(t, rec.Body.String(), "hello")
		})
	}
}

func TestOpenAIResponsesClientPrivacyFailureDoesNotRepeatForward(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			const request = `{"model":"public-A","stream":true,"input":"hello"}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"rid_partial"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"private-C\"}}\n\n" +
						"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
						"data: {\"type\":\"response.usage\",\"usage\":{\"input_tokens\":11,\"output_tokens\":3}}\n\n" +
						"data: {\"type\":\"response.failed\",\"response\":{\"model\":\"private-C\"}\n\n")),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-token", "model_mapping": map[string]any{"public-A": "sent-B"}},
				Extra:       map[string]any{"openai_passthrough": passthrough},
			}
			result, err := svc.Forward(context.Background(), c, account, []byte(request))
			require.ErrorIs(t, err, errOpenAIClientPayload)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "public-A", result.Model)
			if passthrough {
				// Ordinary passthrough deliberately bypasses account model mapping.
				require.Equal(t, "public-A", gjson.GetBytes(upstream.bodies[0], "model").String())
				require.Empty(t, result.BillingModel)
				require.Empty(t, result.UpstreamModel)
			} else {
				require.Equal(t, "sent-B", gjson.GetBytes(upstream.bodies[0], "model").String())
				require.Equal(t, "sent-B", result.BillingModel)
				require.Equal(t, "sent-B", result.UpstreamModel)
			}
			require.Equal(t, "private-C", result.UpstreamResponseModel)
			require.Equal(t, OpenAIUsage{InputTokens: 11, OutputTokens: 3}, result.Usage)
			require.Contains(t, rec.Body.String(), "response.failed")
			require.NotContains(t, rec.Body.String(), "private-C")
		})
	}
}

func TestOpenAIResponsesClientPrivacyRejectsInvalidPublicModelBeforeForward(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-token"}}
	_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"invalid\nmodel","input":"hello"}`))
	require.ErrorIs(t, err, errOpenAIClientPayload)
	require.Empty(t, upstream.requests)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "model", gjson.Get(rec.Body.String(), "error.param").String())
}

func TestOpenAIResponsesClientPrivacyMalformedBufferedSSEDoesNotFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, compact := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%v/compact=%v", passthrough, compact), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				path := "/v1/responses"
				if compact {
					path += "/compact"
				}
				const request = `{"model":"public-A","stream":false,"input":"hello"}`
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(request))
				body := "data: {\"type\":\"response.created\",\"response\":{\"model\":\"private-C\",\"usage\":{\"input_tokens\":13,\"output_tokens\":7}}}\n\n" +
					"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"private-C rate limit reached\"}}\n\n"
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": {"text/event-stream"}, "Openai-Model": {"private-C"}}, Body: io.NopCloser(strings.NewReader(body)),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream,
					responseHeaderFilter: responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{Enabled: true, AdditionalAllowed: []string{"openai-model"}}),
				}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "test-token"}, Extra: map[string]any{"openai_passthrough": passthrough},
				}
				result, err := svc.Forward(context.Background(), c, account, []byte(request))
				require.ErrorIs(t, err, errOpenAIClientPayload)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, OpenAIUsage{InputTokens: 13, OutputTokens: 7}, result.Usage)
				require.Equal(t, "private-C", result.UpstreamResponseModel)
				require.Equal(t, http.StatusBadGateway, rec.Code)
				require.True(t, gjson.ValidBytes(rec.Body.Bytes()))
				require.NotContains(t, rec.Body.String(), "private-C")
				require.Empty(t, rec.Header().Get("Openai-Model"))
			})
		}
	}
}

func TestOpenAIResponsesClientPrivacyOversizeAfterBareErrorEndsOnce(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_once\",\"model\":\"private-C\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.usage\",\"usage\":{\"input_tokens\":11,\"output_tokens\":3}}\n\n" +
		"data: {\"type\":\"error\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"private-C rate limit reached\"}}\n\n" +
		"data: {\n" + strings.Repeat("data: \"large_field\": \"still arriving\",\n", 20)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 256}}}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "public-A", "sent-B")
	require.ErrorIs(t, err, errOpenAIClientPayload)
	require.NotNil(t, result)
	require.Equal(t, OpenAIUsage{InputTokens: 11, OutputTokens: 3}, *result.usage)
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
	require.NotContains(t, rec.Body.String(), "private-C")
}
