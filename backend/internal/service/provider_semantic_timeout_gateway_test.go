package service

// [provider-semantic-timeout] 端到端落点测试：每类下游协议一条正向用例 + 反向用例。
// 移除该 workaround 时整文件一并删除。

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newSemanticTimeoutGatewayTestService() *GatewayService {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	return &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		rateLimitService:     &RateLimitService{},
	}
}

func newSemanticTimeoutOpenAITestService() *OpenAIGatewayService {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	return &OpenAIGatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
	}
}

func semanticTimeoutAnthropicAccount() *Account {
	return &Account{ID: 901, Name: "semantic-timeout-test", Platform: PlatformAnthropic, Type: AccountTypeOAuth}
}

func semanticTimeoutOpenAIAccount() *Account {
	return &Account{ID: 902, Name: "semantic-timeout-openai", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
}

// semanticTimeoutOutOfScopeAccount 用于反向用例：usage 完全命中也必须放行。
func semanticTimeoutOutOfScopeAccount() *Account {
	return &Account{ID: 903, Name: "semantic-timeout-grok", Platform: PlatformGrok, Type: AccountTypeAPIKey}
}

func semanticTimeoutResponse(body string, contentType string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newSemanticTimeoutContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, recorder
}

// anthropicTimeoutStream 是上游"假成功"占位响应的最小 Anthropic SSE 形态：
// message_start 带 cache_read=1000，终局 message_delta 带 output=1000。
const anthropicTimeoutStream = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":0,"cache_read_input_tokens":1000,"output_tokens":0}}}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1000}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

// anthropicNormalStream 与上面结构相同，仅 cache_read 差 1，用于反向断言。
const anthropicNormalStream = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":0,"cache_read_input_tokens":999,"output_tokens":0}}}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1000}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

const anthropicTimeoutJSON = `{"type":"message","id":"msg_1","model":"claude-test","content":[{"type":"text","text":"placeholder"}],"usage":{"input_tokens":0,"cache_read_input_tokens":1000,"output_tokens":1000}}`

// ---------- Anthropic 网关（GatewayService）----------

func TestSemanticTimeoutAnthropicNonStreamingReturns502(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")

	usage, err := newSemanticTimeoutGatewayTestService().handleNonStreamingResponse(
		context.Background(), semanticTimeoutResponse(anthropicTimeoutJSON, "application/json"), c,
		semanticTimeoutAnthropicAccount(), "claude-test", "claude-test",
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, usage, "必须不返回 usage，否则会被计费")
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
	require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
}

func TestSemanticTimeoutAnthropicNonStreamingRecordsUpstreamBody(t *testing.T) {
	c, _ := newSemanticTimeoutContext("/v1/messages")

	_, err := newSemanticTimeoutGatewayTestService().handleNonStreamingResponse(
		context.Background(), semanticTimeoutResponse(anthropicTimeoutJSON, "application/json"), c,
		semanticTimeoutAnthropicAccount(), "claude-test", "claude-test",
	)
	require.ErrorIs(t, err, errProviderSemanticTimeout)

	detail, ok := c.Get(OpsUpstreamErrorDetailKey)
	require.True(t, ok, "上游原始内容必须进入 ops upstream_error_detail")
	detailText, _ := detail.(string)
	require.Contains(t, detailText, "placeholder", "必须能看到上游实际返回的内容")
	require.LessOrEqual(t, len(detailText), providerSemanticTimeoutCaptureMax)

	// wire 已固化为 502 时仍标记 in-band 失败，保证后台可检索。
	streamErrs := GetOpsStreamErrors(c)
	require.NotEmpty(t, streamErrs)
	require.Equal(t, providerSemanticTimeoutCode, streamErrs[0].Code)
}

func TestSemanticTimeoutAnthropicStreamingRejectsAtTerminal(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")

	result, err := newSemanticTimeoutGatewayTestService().handleStreamingResponse(
		context.Background(), semanticTimeoutResponse(anthropicTimeoutStream, "text/event-stream"), c,
		semanticTimeoutAnthropicAccount(), time.Now(), "claude-test", "claude-test", false,
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result, "必须不返回 streamingResult，否则部分 usage 会被计费")
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)

	detail, ok := c.Get(OpsUpstreamErrorDetailKey)
	require.True(t, ok)
	detailText, _ := detail.(string)
	require.Contains(t, detailText, "message_start", "捕获必须包含首帧")
	require.Contains(t, detailText, "message_delta", "捕获必须包含终局 usage 帧")
}

func TestSemanticTimeoutAnthropicStreamingNormalResponsePassesThrough(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")

	result, err := newSemanticTimeoutGatewayTestService().handleStreamingResponse(
		context.Background(), semanticTimeoutResponse(anthropicNormalStream, "text/event-stream"), c,
		semanticTimeoutAnthropicAccount(), time.Now(), "claude-test", "claude-test", false,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 999, result.usage.CacheReadInputTokens)
	require.Equal(t, 1000, result.usage.OutputTokens)
	require.NotContains(t, recorder.Body.String(), providerSemanticTimeoutCode)
	require.Contains(t, recorder.Body.String(), "message_stop", "正常响应必须原样透传")
}

func TestSemanticTimeoutOutOfScopePlatformPassesThrough(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")

	usage, err := newSemanticTimeoutGatewayTestService().handleNonStreamingResponse(
		context.Background(), semanticTimeoutResponse(anthropicTimeoutJSON, "application/json"), c,
		semanticTimeoutOutOfScopeAccount(), "claude-test", "claude-test",
	)

	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "placeholder", "范围外账号必须逐字节透传")
}

func TestPartialStreamUsageSkipsProviderSemanticTimeout(t *testing.T) {
	result := partialStreamUsageResult(
		nil,
		&http.Response{Header: http.Header{"x-request-id": []string{"test"}}},
		&streamingResult{usage: &ClaudeUsage{CacheReadInputTokens: 1000, OutputTokens: 1000}},
		"claude-test", "claude-test", time.Now(), errProviderSemanticTimeout,
	)

	require.Nil(t, result, "sentinel 必须绕过部分 usage 计费")
}

// ---------- Anthropic API-Key 透传 ----------

func newSemanticTimeoutPassthroughService(upstream *anthropicHTTPUpstreamRecorder) *GatewayService {
	svc := newSemanticTimeoutGatewayTestService()
	svc.httpUpstream = upstream
	svc.deferredService = &DeferredService{}
	return svc
}

func TestSemanticTimeoutAnthropicPassthroughNonStreaming(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")
	parsed := &ParsedRequest{
		Body:  NewRequestBodyRef([]byte(`{"model":"claude-test","stream":false,"messages":[{"role":"user","content":"hi"}]}`)),
		Model: "claude-test",
	}
	upstream := &anthropicHTTPUpstreamRecorder{resp: semanticTimeoutResponse(anthropicTimeoutJSON, "application/json")}

	result, err := newSemanticTimeoutPassthroughService(upstream).Forward(
		context.Background(), c, newAnthropicAPIKeyAccountForTest(), parsed,
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

func TestSemanticTimeoutAnthropicPassthroughStreaming(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef([]byte(`{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hi"}]}`)),
		Model:  "claude-test",
		Stream: true,
	}
	upstream := &anthropicHTTPUpstreamRecorder{resp: semanticTimeoutResponse(anthropicTimeoutStream, "text/event-stream")}

	result, err := newSemanticTimeoutPassthroughService(upstream).Forward(
		context.Background(), c, newAnthropicAPIKeyAccountForTest(), parsed,
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

// ---------- Anthropic 上游 → CC / Responses 下游转换路径 ----------

func TestSemanticTimeoutForwardAsChatCompletionsBuffered(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/chat/completions")

	result, err := newSemanticTimeoutGatewayTestService().handleCCBufferedFromAnthropic(
		semanticTimeoutResponse(anthropicTimeoutStream, "text/event-stream"), c,
		semanticTimeoutAnthropicAccount(), "claude-test", "claude-test", nil, time.Now(),
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

func TestSemanticTimeoutForwardAsChatCompletionsStreaming(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/chat/completions")

	result, err := newSemanticTimeoutGatewayTestService().handleCCStreamingFromAnthropic(
		semanticTimeoutResponse(anthropicTimeoutStream, "text/event-stream"), c,
		semanticTimeoutAnthropicAccount(), "claude-test", "claude-test", nil, time.Now(), true,
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	// 流式已提交 200 响应头，只能补一帧带内错误。
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

func TestSemanticTimeoutForwardAsResponsesBuffered(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/responses")

	result, err := newSemanticTimeoutGatewayTestService().handleResponsesBufferedStreamingResponse(
		semanticTimeoutResponse(anthropicTimeoutStream, "text/event-stream"), c,
		semanticTimeoutAnthropicAccount(), "claude-test", "claude-test", nil, time.Now(),
		apicompat.ResponsesClientToolMapping{},
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

func TestSemanticTimeoutForwardAsResponsesStreaming(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/responses")

	result, err := newSemanticTimeoutGatewayTestService().handleResponsesStreamingResponse(
		semanticTimeoutResponse(anthropicTimeoutStream, "text/event-stream"), c,
		semanticTimeoutAnthropicAccount(), "claude-test", "claude-test", nil, time.Now(),
		apicompat.ResponsesClientToolMapping{},
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Contains(t, recorder.Body.String(), "response.failed", "Responses 下游必须收到 response.failed 终止事件")
}

// ---------- OpenAI 网关 ----------

// ccTimeoutStream 是 chat/completions 上游的占位响应形态。
const ccTimeoutStream = `data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-test","choices":[{"index":0,"delta":{"content":"placeholder"}}]}` + "\n\n" +
	`data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-test","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":1000,"total_tokens":2000,"prompt_tokens_details":{"cached_tokens":1000}}}` + "\n\n" +
	"data: [DONE]\n\n"

const ccTimeoutJSON = `{"id":"c1","object":"chat.completion","model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"placeholder"}}],"usage":{"prompt_tokens":1000,"completion_tokens":1000,"total_tokens":2000,"prompt_tokens_details":{"cached_tokens":1000}}}`

// responsesTimeoutJSON 是 /v1/responses 上游的占位响应形态。
const responsesTimeoutJSON = `{"id":"resp_1","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"placeholder"}]}],"usage":{"input_tokens":1000,"output_tokens":1000,"total_tokens":2000,"input_tokens_details":{"cached_tokens":1000}}}`

func TestSemanticTimeoutOpenAIRawChatCompletionsStreaming(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/chat/completions")

	result, err := newSemanticTimeoutOpenAITestService().streamRawChatCompletions(
		c, semanticTimeoutResponse(ccTimeoutStream, "text/event-stream"), semanticTimeoutOpenAIAccount(),
		"gpt-test", "gpt-test", "gpt-test", nil, nil, time.Now(), 0,
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)

	detail, ok := c.Get(OpsUpstreamErrorDetailKey)
	require.True(t, ok)
	detailText, _ := detail.(string)
	require.Contains(t, detailText, "placeholder", "必须记录上游返回的内容")
}

func TestSemanticTimeoutOpenAIRawChatCompletionsBuffered(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/chat/completions")

	result, err := newSemanticTimeoutOpenAITestService().bufferRawChatCompletions(
		c, semanticTimeoutResponse(ccTimeoutJSON, "application/json"), semanticTimeoutOpenAIAccount(),
		"gpt-test", "gpt-test", "gpt-test", nil, nil, time.Now(),
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

func TestSemanticTimeoutOpenAIRawChatCompletionsNormalPassesThrough(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/chat/completions")
	// cache_read 差 1：必须正常透传。
	normalJSON := strings.Replace(ccTimeoutJSON, `"cached_tokens":1000`, `"cached_tokens":999`, 1)

	result, err := newSemanticTimeoutOpenAITestService().bufferRawChatCompletions(
		c, semanticTimeoutResponse(normalJSON, "application/json"), semanticTimeoutOpenAIAccount(),
		"gpt-test", "gpt-test", "gpt-test", nil, nil, time.Now(),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "placeholder")
}

// ---------- /v1/messages 与 /v1/responses 的 chat_completions 回退（本次新增覆盖）----------

func TestSemanticTimeoutMessagesChatFallbackStreaming(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")

	result, err := newSemanticTimeoutOpenAITestService().streamChatCompletionsAsAnthropic(
		c, semanticTimeoutResponse(ccTimeoutStream, "text/event-stream"), semanticTimeoutOpenAIAccount(),
		"claude-test", "gpt-test", "gpt-test", nil, nil, time.Now(),
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

func TestSemanticTimeoutMessagesChatFallbackBuffered(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/messages")

	result, err := newSemanticTimeoutOpenAITestService().bufferChatCompletionsAsAnthropic(
		c, semanticTimeoutResponse(ccTimeoutJSON, "application/json"), semanticTimeoutOpenAIAccount(),
		"claude-test", "gpt-test", "gpt-test", nil, nil, time.Now(),
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

func TestSemanticTimeoutResponsesChatFallbackStreaming(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/responses")

	result, err := newSemanticTimeoutOpenAITestService().streamChatCompletionsAsResponses(
		c, semanticTimeoutResponse(ccTimeoutStream, "text/event-stream"), semanticTimeoutOpenAIAccount(),
		"gpt-test", nil, nil, false, nil, "gpt-test", "gpt-test", nil, nil, time.Now(),
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Contains(t, recorder.Body.String(), "response.failed")
}

func TestSemanticTimeoutResponsesChatFallbackBuffered(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/responses")

	result, err := newSemanticTimeoutOpenAITestService().bufferChatCompletionsAsResponses(
		c, semanticTimeoutResponse(ccTimeoutJSON, "application/json"), semanticTimeoutOpenAIAccount(),
		"gpt-test", nil, nil, false, nil, "gpt-test", "gpt-test", nil, nil, time.Now(),
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), providerSemanticTimeoutCode)
}

// compact 心跳已把响应头提交为 200 时，非流式落点不能回 JSON+502，必须改走
// response.failed 终止事件——该分支由 writeOpenAINonStreamingProtocolError 内部
// 判定，所以 fresh/in-band 两条路都要交给它，否则这里会静默什么都不写。
func TestSemanticTimeoutOpenAINonStreamingKeepaliveCommittedWritesTerminalEvent(t *testing.T) {
	c, recorder := newSemanticTimeoutContext("/v1/responses")
	c.Set(openAICompactClientStreamKey, true)
	stop := StartOpenAICompactSSEKeepalive(c, time.Millisecond)
	defer stop()
	// 等心跳真正提交响应头（started=true），模拟"上游等待期间已下发心跳"。
	require.Eventually(t, func() bool { return c.Writer.Written() }, time.Second, 2*time.Millisecond)

	result, err := newSemanticTimeoutOpenAITestService().handleNonStreamingResponse(
		context.Background(), semanticTimeoutResponse(responsesTimeoutJSON, "application/json"), c,
		semanticTimeoutOpenAIAccount(), "gpt-test", "gpt-test",
	)

	require.ErrorIs(t, err, errProviderSemanticTimeout)
	require.Nil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code, "心跳已固化 200，状态码不可改")
	require.Contains(t, recorder.Body.String(), "response.failed",
		"必须补 response.failed 终止事件，而不是静默结束")
}

// ---------- Layer 2：计费兜底 ----------
func TestSemanticTimeoutBillingGuardSkipsAnthropicRecordUsage(t *testing.T) {
	// recordUsageCore 依赖仓储；命中时必须在其之前早退，因此空 service 也不会 panic。
	svc := &GatewayService{}
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Account: semanticTimeoutAnthropicAccount(),
		Result: &ForwardResult{
			Model: "claude-test",
			Usage: ClaudeUsage{CacheReadInputTokens: 1000, OutputTokens: 1000},
		},
	})
	require.NoError(t, err, "命中时静默跳过计费")
}

func TestSemanticTimeoutBillingGuardSkipsOpenAIRecordUsage(t *testing.T) {
	svc := &OpenAIGatewayService{}
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Account: semanticTimeoutOpenAIAccount(),
		Result: &OpenAIForwardResult{
			Model: "gpt-test",
			Usage: OpenAIUsage{CacheReadInputTokens: 1000, OutputTokens: 1000},
		},
	})
	require.NoError(t, err, "命中时静默跳过计费")
}
