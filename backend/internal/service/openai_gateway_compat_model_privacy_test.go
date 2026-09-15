package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newOpenAICompatPrivacyContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	beginUpstreamResponseModelObservation(c)
	return c, recorder
}

func forwardOpenAICompatPrivacyRoute(route string, stream bool, svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account, original, billing, upstream string) (*OpenAIForwardResult, error) {
	switch route {
	case "chat":
		if stream {
			return svc.handleChatStreamingResponse(resp, c, account, original, billing, upstream, time.Now(), 0)
		}
		return svc.handleChatBufferedStreamingResponse(resp, c, account, original, billing, upstream, time.Now())
	case "messages":
		if stream {
			return svc.handleAnthropicStreamingResponse(resp, c, account, original, billing, upstream, time.Now())
		}
		return svc.handleAnthropicBufferedStreamingResponse(resp, c, account, original, billing, upstream, time.Now())
	case "messages_chat_fallback":
		if stream {
			return svc.streamChatCompletionsAsAnthropic(c, resp, account, original, billing, upstream, nil, nil, time.Now())
		}
		return svc.bufferChatCompletionsAsAnthropic(c, resp, account, original, billing, upstream, nil, nil, time.Now())
	case "responses_chat_fallback":
		if stream {
			return svc.streamChatCompletionsAsResponses(c, resp, account, original, nil, nil, false, nil, billing, upstream, nil, nil, time.Now())
		}
		return svc.bufferChatCompletionsAsResponses(c, resp, account, original, nil, nil, false, nil, billing, upstream, nil, nil, time.Now())
	default:
		if stream {
			return svc.streamRawChatCompletions(c, resp, account, original, billing, upstream, nil, nil, time.Now(), 0)
		}
		return svc.bufferRawChatCompletions(c, resp, account, original, billing, upstream, nil, nil, time.Now())
	}
}

func openAICompatPrivacyResponseBody(route string, stream bool, declared string) string {
	if route == "raw" || route == "messages_chat_fallback" || route == "responses_chat_fallback" {
		usage := `"usage":{"prompt_tokens":19,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3}}`
		if !stream {
			return fmt.Sprintf(`{"id":"chatcmpl_privacy","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"text mentions private-model-C"},"finish_reason":"stop"}],%s}`, declared, usage)
		}
		return fmt.Sprintf("data: {\"id\":\"chatcmpl_privacy\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"text mentions private-model-C\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl_privacy\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],%s}\n\ndata: [DONE]\n\n", declared, declared, usage)
	}
	terminal := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_privacy","object":"response","model":%q,"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"text mentions private-model-C"}]}],"usage":{"input_tokens":19,"output_tokens":7,"input_tokens_details":{"cached_tokens":3}}}}`, declared)
	return fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_privacy\",\"model\":%q}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"text mentions private-model-C\"}\n\ndata: %s\n\n", declared, terminal)
}

func TestOpenAICompatClientModelPrivacyPreservesUsageAndObservation(t *testing.T) {
	scenarios := []struct{ name, original, billing, declared string }{
		{"mapped_equal_response", "public-model-A", "upstream-model-B", "upstream-model-B"},
		{"mapped_different_response", "public-model-A", "upstream-model-B", "private-model-C"},
		{"unmapped_different_response", "public-model-A", "public-model-A", "private-model-C"},
		{"composite", "route-model-B", "upstream-model-B", "private-model-C"},
	}
	for _, route := range []string{"chat", "messages", "messages_chat_fallback", "responses_chat_fallback", "raw"} {
		for _, stream := range []bool{false, true} {
			for _, scenario := range scenarios {
				t.Run(fmt.Sprintf("%s/stream_%t/%s", route, stream, scenario.name), func(t *testing.T) {
					c, recorder := newOpenAICompatPrivacyContext()
					if scenario.name == "composite" {
						c.Request = c.Request.WithContext(WithCompositeRouteDecision(c.Request.Context(), CompositeRouteDecision{
							Matched: true, PublicModel: "public-model-A", UpstreamModel: scenario.original, TargetPlatform: PlatformOpenAI,
						}))
					}
					svc := &OpenAIGatewayService{cfg: &config.Config{}, responseHeaderFilter: responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
						Enabled: true, AdditionalAllowed: []string{"x-upstream-model", "openai-model"},
					})}
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
						"X-Request-Id": {"privacy-request"}, "Openai-Model": {scenario.declared}, "X-Upstream-Model": {scenario.declared},
						"Retry-After": {"2"}, "X-Reasoning-Included": {"true"},
					}, Body: io.NopCloser(strings.NewReader(openAICompatPrivacyResponseBody(route, stream, scenario.declared)))}
					result, err := forwardOpenAICompatPrivacyRoute(route, stream, svc, c, resp, &Account{ID: 1, Platform: PlatformOpenAI}, scenario.original, scenario.billing, scenario.billing)
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, scenario.original, result.Model)
					require.Equal(t, scenario.billing, result.BillingModel)
					require.Equal(t, scenario.billing, result.UpstreamModel)
					require.Equal(t, scenario.declared, observedUpstreamResponseModel(c))
					require.False(t, observedUpstreamResponseModelConflict(c))
					require.Equal(t, 19, result.Usage.InputTokens)
					require.Equal(t, 7, result.Usage.OutputTokens)
					require.Equal(t, 3, result.Usage.CacheReadInputTokens)
					require.Contains(t, recorder.Body.String(), "text mentions private-model-C")
					models := 0
					assertModel := func(payload []byte) {
						for _, path := range []string{"model", "response.model", "message.model"} {
							if value := gjson.GetBytes(payload, path); value.Exists() {
								require.Equal(t, "public-model-A", value.String(), path)
								models++
							}
						}
					}
					if stream {
						forEachOpenAISSEDataPayload(recorder.Body.String(), assertModel)
					} else {
						assertModel(recorder.Body.Bytes())
					}
					require.Positive(t, models)
					require.Empty(t, recorder.Header().Get("Openai-Model"))
					require.Empty(t, recorder.Header().Get("X-Upstream-Model"))
					require.Equal(t, "privacy-request", recorder.Header().Get("X-Request-Id"))
					require.Equal(t, "2", recorder.Header().Get("Retry-After"))
					require.Equal(t, "true", recorder.Header().Get("X-Reasoning-Included"))
				})
			}
		}
	}
}

func TestOpenAICompatClientModelPrivacyRawMultilineSSEPreservesProtocolAndTools(t *testing.T) {
	c, recorder := newOpenAICompatPrivacyContext()
	SetOpenAIClientRequestedModel(c, "public-model-A")
	modelFreeDelta := `{"id":"chatcmpl_tool","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_keep","type":"function","function":{"name":"lookup","arguments":"{\"model\":\"private-model-C\"}"}}]}}]}`
	body := ": keepalive\r\n\r\nid: event-1\r\nevent: message\r\nretry: 1200\r\n" +
		`data: {"id":"chatcmpl_tool","model":"private-model-C",` + "\r\n" +
		`data: "response":{"model":"private-model-C"},"message":{"model":"private-model-C"},"choices":[{"index":0,"delta":{"role":"assistant"}}],"opaque":{"model":"private-model-C","integer":9007199254740993}}` + "\r\n\r\n" +
		"data: " + modelFreeDelta + "\r\n\r\n" +
		`data: {"id":"chatcmpl_tool","model":"private-model-C","choices":[],"usage":{"prompt_tokens":19,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3}}}` + "\r\n\r\ndata: [DONE]\r\n\r\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(iotest.OneByteReader(strings.NewReader(body)))}
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	result, err := svc.streamRawChatCompletions(c, resp, &Account{ID: 1, Platform: PlatformOpenAI}, "route-model-B", "upstream-model-B", "upstream-model-B", nil, nil, time.Now(), 0)
	require.NoError(t, err)
	require.Equal(t, 19, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, "private-model-C", result.UpstreamResponseModel)
	require.Contains(t, recorder.Body.String(), ": keepalive")
	require.Contains(t, recorder.Body.String(), "id: event-1")
	require.Contains(t, recorder.Body.String(), "event: message")
	require.Contains(t, recorder.Body.String(), "retry: 1200")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
	var payloads [][]byte
	forEachOpenAISSEDataPayload(recorder.Body.String(), func(payload []byte) { payloads = append(payloads, payload) })
	require.Len(t, payloads, 3)
	for _, path := range []string{"model", "response.model", "message.model"} {
		require.Equal(t, "public-model-A", gjson.GetBytes(payloads[0], path).String())
	}
	require.Equal(t, "private-model-C", gjson.GetBytes(payloads[0], "opaque.model").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(payloads[0], "opaque.integer").Raw)
	require.JSONEq(t, modelFreeDelta, string(payloads[1]))
	require.False(t, gjson.GetBytes(payloads[1], "model").Exists())
}

func TestOpenAICompatClientModelPrivacyRawFailureKeepsUsageAndDoesNotFailover(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			c, recorder := newOpenAICompatPrivacyContext()
			// The public model is missing in this direct output call. The upstream
			// response must not substitute C, and already parsed usage is retained.
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(openAICompatPrivacyResponseBody("raw", stream, "private-model-C")))}
			if stream {
				resp.Body = io.NopCloser(strings.NewReader(`data: {"model":"private-model-C","choices":[],"usage":{"prompt_tokens":19,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3}}}` + "\n\ndata: [DONE]\n\n"))
			}
			result, err := forwardOpenAICompatPrivacyRoute("raw", stream, &OpenAIGatewayService{cfg: &config.Config{}}, c, resp, &Account{ID: 1, Platform: PlatformOpenAI}, "", "upstream-model-B", "upstream-model-B")
			require.ErrorIs(t, err, errOpenAIClientPayload)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.NotNil(t, result)
			require.Equal(t, 19, result.Usage.InputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
			require.Equal(t, "private-model-C", result.UpstreamResponseModel)
			require.Equal(t, http.StatusBadGateway, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "private-model-C")
		})
	}
}

func TestOpenAICompatClientModelPrivacyErrorsKeepOriginalDiagnostics(t *testing.T) {
	for _, route := range []string{"chat", "messages", "raw"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", route, stream), func(t *testing.T) {
				c, recorder := newOpenAICompatPrivacyContext()
				SetOpenAIClientRequestedModel(c, "public-model-A")
				errorJSON := `{"type":"invalid_request_error","code":"invalid_function_parameters","message":"Invalid schema not allowed for private-model-C"}`
				body := `{"type":"response.failed","response":{"id":"resp_error","model":"private-model-C","status":"failed","error":` + errorJSON + `,"output":[],"usage":{"input_tokens":19,"output_tokens":7}}}`
				if route == "raw" {
					body = `{"model":"private-model-C","error":` + errorJSON + `,"usage":{"prompt_tokens":19,"completion_tokens":7}}`
				}
				if stream {
					prefix := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_error\",\"model\":\"private-model-C\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial output\"}\n\n"
					if route == "raw" {
						prefix = "data: {\"model\":\"private-model-C\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial output\"}}]}\n\n"
					}
					body = prefix + "data: " + body + "\n\ndata: [DONE]\n\n"
				} else if route != "raw" {
					body = "data: " + body + "\n\n"
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
				_, err := forwardOpenAICompatPrivacyRoute(route, stream, &OpenAIGatewayService{cfg: &config.Config{}}, c, resp, &Account{ID: 1, Platform: PlatformOpenAI}, "route-model-B", "upstream-model-B", "upstream-model-B")
				if route != "raw" {
					require.Error(t, err)
					require.Contains(t, err.Error(), "private-model-C", "internal diagnostics must remain original")
				} else {
					require.NoError(t, err)
				}
				require.NotContains(t, recorder.Body.String(), "private-model-C")
				require.Contains(t, recorder.Body.String(), "Invalid schema for function parameters.")
				if stream {
					require.Contains(t, recorder.Body.String(), "partial output")
				}
				events, exists := c.Get(OpsUpstreamErrorsKey)
				require.True(t, exists)
				upstreamEvents, ok := events.([]*OpsUpstreamErrorEvent)
				require.True(t, ok)
				require.NotEmpty(t, upstreamEvents)
				require.Contains(t, upstreamEvents[len(upstreamEvents)-1].Message, "private-model-C")
			})
		}
	}
}

func TestOpenAICompatClientModelPrivacyDoesNotChangeOtherPlatforms(t *testing.T) {
	for _, route := range []string{"chat", "messages", "messages_chat_fallback", "responses_chat_fallback", "raw"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", route, stream), func(t *testing.T) {
				c, recorder := newOpenAICompatPrivacyContext()
				SetOpenAIClientRequestedModel(c, "public-model-A")
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Upstream-Model": {"private-model-C"}}, Body: io.NopCloser(strings.NewReader(openAICompatPrivacyResponseBody(route, stream, "private-model-C")))}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, responseHeaderFilter: responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
					Enabled: true, AdditionalAllowed: []string{"x-upstream-model"},
				})}
				result, err := forwardOpenAICompatPrivacyRoute(route, stream, svc, c, resp, &Account{ID: 1, Platform: PlatformDeepseek}, "route-model-B", "upstream-model-B", "upstream-model-B")
				require.NoError(t, err)
				require.NotNil(t, result)
				expectedModel := "route-model-B"
				if route == "raw" {
					expectedModel = "private-model-C"
				}
				require.Contains(t, recorder.Body.String(), fmt.Sprintf(`"model":%q`, expectedModel))
				require.NotContains(t, recorder.Body.String(), "public-model-A")
				require.Equal(t, "private-model-C", recorder.Header().Get("X-Upstream-Model"))
			})
		}
	}
}

func TestOpenAICompatClientModelPrivacyKeepsPublicModelAcrossResponsesFallback(t *testing.T) {
	c, recorder := newOpenAICompatPrivacyContext()
	c.Request = c.Request.WithContext(WithCompositeRouteDecision(c.Request.Context(), CompositeRouteDecision{
		Matched: true, PublicModel: "public-model-A", UpstreamModel: "route-model-B", TargetPlatform: PlatformOpenAI,
	}))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"not_found_error","message":"Responses endpoint is not supported"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(openAICompatPrivacyResponseBody("raw", false, "private-model-C")))},
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}, httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "base_url": "http://upstream.example", "model_mapping": map[string]any{"route-model-B": "upstream-model-B"},
	}}
	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(`{"model":"route-model-B","messages":[{"role":"user","content":"hello"}]}`), "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
	require.Equal(t, "/v1/chat/completions", upstream.requests[1].URL.Path)
	for _, body := range upstream.bodies {
		require.Equal(t, "upstream-model-B", gjson.GetBytes(body, "model").String())
	}
	require.Equal(t, "public-model-A", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
	require.Equal(t, "route-model-B", result.Model)
	require.Equal(t, "upstream-model-B", result.BillingModel)
	require.Equal(t, "private-model-C", result.UpstreamResponseModel)
	SetOpenAIClientRequestedModel(c, "later-model")
	require.Equal(t, "public-model-A", openAIClientRequestedModel(c, "fallback-model"))
}

func TestOpenAICompatClientModelPrivacyResponsesToChatFallbackKeepsCompositeModel(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			c, recorder := newOpenAICompatPrivacyContext()
			body := []byte(fmt.Sprintf(`{"model":"route-model-B","input":"hello","stream":%t}`, stream))
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			c.Request = c.Request.WithContext(WithCompositeRouteDecision(c.Request.Context(), CompositeRouteDecision{
				Matched: true, PublicModel: "public-model-A", UpstreamModel: "route-model-B", TargetPlatform: PlatformOpenAI,
			}))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{},
				Body: io.NopCloser(strings.NewReader(openAICompatPrivacyResponseBody("raw", stream, "private-model-C"))),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}, httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"api_key": "test-key", "base_url": "http://upstream.example", "model_mapping": map[string]any{"route-model-B": "upstream-model-B"},
			}, Extra: map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)}}
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
			require.Equal(t, "upstream-model-B", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, "route-model-B", result.Model)
			require.Equal(t, "upstream-model-B", result.BillingModel)
			require.Equal(t, "upstream-model-B", result.UpstreamModel)
			require.Equal(t, "private-model-C", observedUpstreamResponseModel(c))
			require.Equal(t, 19, result.Usage.InputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
			require.Equal(t, 3, result.Usage.CacheReadInputTokens)
			require.Contains(t, recorder.Body.String(), "text mentions private-model-C")
			if stream {
				models := 0
				forEachOpenAISSEDataPayload(recorder.Body.String(), func(payload []byte) {
					if model := gjson.GetBytes(payload, "response.model"); model.Exists() {
						require.Equal(t, "public-model-A", model.String())
						models++
					}
				})
				require.Positive(t, models)
				require.Contains(t, recorder.Body.String(), "event: response.completed")
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
			} else {
				require.Equal(t, "public-model-A", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
			}
			SetOpenAIClientRequestedModel(c, "later-model")
			require.Equal(t, "public-model-A", openAIClientRequestedModel(c, "fallback-model"))
		})
	}
}

func TestOpenAICompatClientModelPrivacyMalformedRawStreamStopsWithoutReplay(t *testing.T) {
	c, recorder := newOpenAICompatPrivacyContext()
	upstreamBody := `data: {"model":"private-model-C","choices":[{"index":0,"delta":{"content":"partial output"}}],"usage":{"prompt_tokens":19,"completion_tokens":7}}` + "\n\n" +
		`data: {"model":"must-not-leak",broken}` + "\n\ndata: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(upstreamBody))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}, httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "base_url": "http://upstream.example"}}
	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, []byte(`{"model":"public-model-A","messages":[{"role":"user","content":"hello"}],"stream":true}`), "")
	require.ErrorIs(t, err, errOpenAIClientPayload)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Len(t, upstream.requests, 1)
	require.NotNil(t, result)
	require.Equal(t, 19, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, "private-model-C", result.UpstreamResponseModel)
	require.Contains(t, recorder.Body.String(), "partial output")
	require.Contains(t, recorder.Body.String(), "response_processing_error")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

func TestOpenAICompatClientModelPrivacyRawAdjacentDataLines(t *testing.T) {
	c, recorder := newOpenAICompatPrivacyContext()
	body := strings.ReplaceAll(openAICompatPrivacyResponseBody("raw", true, "private-model-C"), "\n\n", "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	result, err := forwardOpenAICompatPrivacyRoute("raw", true, &OpenAIGatewayService{cfg: &config.Config{}}, c, resp, &Account{ID: 1, Platform: PlatformOpenAI}, "public-model-A", "upstream-model-B", "upstream-model-B")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 19, result.Usage.InputTokens)
	require.Equal(t, "private-model-C", result.UpstreamResponseModel)
	require.Contains(t, recorder.Body.String(), `"model":"public-model-A"`)
	require.NotContains(t, recorder.Body.String(), `"model":"private-model-C"`)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
}

func TestOpenAICompatClientModelPrivacyRejectsInvalidPublicModelBeforeUpstream(t *testing.T) {
	for _, route := range []string{"chat", "messages", "raw", "messages_chat_fallback", "responses_chat_fallback"} {
		t.Run(route, func(t *testing.T) {
			c, recorder := newOpenAICompatPrivacyContext()
			body := []byte(`{"model":"bad\u0001model","messages":[{"role":"user","content":"hello"}],"max_tokens":16}`)
			upstream := &httpUpstreamRecorder{err: errors.New("unexpected upstream request")}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
			var err error
			switch route {
			case "chat":
				_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			case "messages":
				_, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
			case "messages_chat_fallback":
				_, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, body, "")
			case "responses_chat_fallback":
				_, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body)
			default:
				_, err = svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
			}
			require.ErrorIs(t, err, errOpenAIClientPayload)
			require.Empty(t, upstream.requests)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestOpenAICompatClientModelPrivacyLocalPolicyKeepsOriginalCause(t *testing.T) {
	for _, route := range []string{"chat", "messages", "raw", "responses_chat_fallback"} {
		t.Run(route, func(t *testing.T) {
			c, recorder := newOpenAICompatPrivacyContext()
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
				ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true,
			})
			c.Request = c.Request.WithContext(ctx)
			body := []byte(`{"model":"public-model-A","messages":[{"role":"user","content":"hello"}],"max_tokens":16}`)
			upstream := &httpUpstreamRecorder{err: errors.New("unexpected upstream request")}
			svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
				ServiceTier: OpenAIFastTierPriority, Action: BetaPolicyActionBlock, Scope: BetaPolicyScopeAll,
				ErrorMessage: "priority blocked for upstream-model-B", ModelWhitelist: []string{"upstream-model-B"}, FallbackAction: BetaPolicyActionPass,
			}}})
			svc.cfg, svc.httpUpstream = &config.Config{}, upstream
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"api_key": "test-key", "model_mapping": map[string]any{"public-model-A": "upstream-model-B"},
			}}
			var err error
			switch route {
			case "chat":
				_, err = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
			case "messages":
				_, err = svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
			case "responses_chat_fallback":
				_, err = svc.forwardResponsesViaRawChatCompletions(ctx, c, account, []byte(`{"model":"public-model-A","input":"hello"}`))
			default:
				_, err = svc.forwardAsRawChatCompletions(ctx, c, account, body, "")
			}
			var blocked *OpenAIFastBlockedError
			require.ErrorAs(t, err, &blocked)
			require.Equal(t, "priority blocked for upstream-model-B", blocked.Message)
			require.Empty(t, upstream.requests)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.True(t, HasOpsClientBusinessLimited(c))
			require.Equal(t, "Request blocked by policy.", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
			require.NotContains(t, recorder.Body.String(), "upstream-model-B")
		})
	}
}

func TestOpenAICompatClientModelPrivacyRawEventLimitKeepsPartialUsage(t *testing.T) {
	c, recorder := newOpenAICompatPrivacyContext()
	body := `data: {"model":"private-model-C","choices":[],"usage":{"prompt_tokens":19,"completion_tokens":7}}` + "\n\n" +
		`data: {"model":"must-not-leak","padding":"` + strings.Repeat("x", 120) + `",` + "\n" +
		`data: "more":"` + strings.Repeat("x", 120) + `"}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 256}}}
	result, err := forwardOpenAICompatPrivacyRoute("raw", true, svc, c, resp, &Account{ID: 1, Platform: PlatformOpenAI}, "public-model-A", "upstream-model-B", "upstream-model-B")
	require.ErrorIs(t, err, errOpenAIClientPayload)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.NotNil(t, result)
	require.Equal(t, 19, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, "private-model-C", result.UpstreamResponseModel)
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
	require.Contains(t, recorder.Body.String(), "response_processing_error")
}

func TestOpenAICompatClientModelPrivacyRawJSONReturnedToStreamRequest(t *testing.T) {
	for _, multiline := range []bool{false, true} {
		t.Run(fmt.Sprintf("multiline_%t", multiline), func(t *testing.T) {
			c, recorder := newOpenAICompatPrivacyContext()
			body := openAICompatPrivacyResponseBody("raw", false, "private-model-C")
			if multiline {
				var formatted bytes.Buffer
				require.NoError(t, json.Indent(&formatted, []byte(body), "", "  "))
				body = formatted.String()
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(iotest.OneByteReader(strings.NewReader(body)))}
			result, err := forwardOpenAICompatPrivacyRoute("raw", true, &OpenAIGatewayService{cfg: &config.Config{}}, c, resp, &Account{ID: 1, Platform: PlatformOpenAI}, "public-model-A", "upstream-model-B", "upstream-model-B")
			require.NoError(t, err)
			require.True(t, result.Stream)
			require.True(t, gjson.ValidBytes(recorder.Body.Bytes()))
			require.Equal(t, "public-model-A", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
			require.Equal(t, "text mentions private-model-C", gjson.GetBytes(recorder.Body.Bytes(), "choices.0.message.content").String())
		})
	}
}
