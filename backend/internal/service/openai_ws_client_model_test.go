package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSClientModels_PreservesSnapshotUntilTerminalWrite(t *testing.T) {
	models := newOpenAIWSClientModels("public-first")
	created := []byte(`{"type":"response.created","response":{"id":"resp_first","model":"private-first"}}`)
	require.True(t, models.observe(coderws.MessageText, created))
	models.queueSessionUpdate([]byte(`{"type":"session.update","session":{"model":"public-session"}}`))
	terminal := []byte(`{"type":"response.completed","response":{"id":"resp_first","model":"private-first"}}`)
	models.markSettled("resp_first")
	require.Equal(t, "public-first", models.modelForPayload(terminal))
	require.Equal(t, "public-session", models.modelForPayload([]byte(`{"type":"session.updated","session":{"model":"private-session"}}`)))
	models.finishTerminal(terminal)
	models.beginTurn("public-next")
	require.False(t, models.observe(coderws.MessageText, terminal), "late terminal must not be billed again")
	require.False(t, models.ownsTerminal(terminal), "late terminal must not unlock the current turn")
	require.Equal(t, "public-first", models.modelForPayload(terminal))
	next := []byte(`{"type":"response.created","response":{"id":"resp_next","model":"private-next"}}`)
	require.True(t, models.observe(coderws.MessageText, next))
	require.Equal(t, "public-next", models.modelForPayload(next))
	unknown := []byte(`{"type":"response.updated","response":{"id":"resp_unknown","model":"private-unknown"}}`)
	require.False(t, models.observe(coderws.MessageText, unknown), "unrelated response must not replace this turn's usage or model")
	require.True(t, models.isUnrelatedResponse(unknown))
	_, err := rewriteOpenAIClientPayload(unknown, models.modelForPayload(unknown))
	require.True(t, IsOpenAIClientPayloadError(err))
	delta := []byte(`{"type":"response.output_text.delta","response_id":"resp_unknown","delta":"private-unknown"}`)
	unchanged, err := rewriteOpenAIClientPayload(delta, models.modelForPayload(delta))
	require.NoError(t, err)
	require.Equal(t, delta, unchanged)
}

func newWSClientModelPrivacyService(t *testing.T, events [][]byte) (*OpenAIGatewayService, *Account, *openAIWSCaptureDialer) {
	t.Helper()
	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	dialer := &openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: events}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	svc := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool,
	}
	account := &Account{
		ID: 5911, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true},
	}
	return svc, account, dialer
}

func TestForwardOpenAIWSV2ClientModelPrivacy_SeparatePublicOutboundObserved(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, mapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream_%t_mapped_%t", stream, mapped), func(t *testing.T) {
				terminal := []byte(`{"type":"response.completed","model":"private-top","response":{"id":"resp_privacy","model":"private-response","output":[{"type":"message","content":[{"type":"output_text","text":"private-response"}]}],"usage":{"input_tokens":17,"output_tokens":5}}}`)
				svc, account, dialer := newWSClientModelPrivacyService(t, [][]byte{terminal})
				if mapped {
					account.Credentials["model_mapping"] = map[string]any{"public-name": "mapped-name"}
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				result, err := svc.Forward(context.Background(), c, account, []byte(fmt.Sprintf(`{"model":"public-name","stream":%t,"input":"hi"}`, stream)))
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "private-response", result.UpstreamResponseModel)
				require.Equal(t, 17, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				payload := recorder.Body.Bytes()
				if stream {
					payload = []byte(strings.TrimSpace(strings.TrimPrefix(string(payload), "data: ")))
					require.Equal(t, "public-name", gjson.GetBytes(payload, "model").String())
					payload = []byte(gjson.GetBytes(payload, "response").Raw)
				}
				require.Equal(t, "public-name", gjson.GetBytes(payload, "model").String())
				require.Equal(t, "private-response", gjson.GetBytes(payload, "output.0.content.0.text").String())
				wantUpstream := "public-name"
				if mapped {
					wantUpstream = "mapped-name"
				}
				require.Equal(t, wantUpstream, dialer.conn.lastWrite["model"])
				require.Equal(t, 1, dialer.DialCount())
			})
		}
	}
}

func TestForwardOpenAIWSV2ClientModelPrivacy_MalformedEventKeepsUsageWithoutRetry(t *testing.T) {
	malformed := []byte(`{"type":"response.completed","response":{"id":"resp_invalid","model":"private-response","usage":{"input_tokens":17,"output_tokens":5}}`)
	preamble := []byte(`{"type":"response.in_progress","response":{"id":"resp_invalid","model":"private-response","usage":{"input_tokens":17,"output_tokens":5}}}`)
	svc, account, dialer := newWSClientModelPrivacyService(t, [][]byte{preamble, malformed})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"public-name","stream":true,"input":"hi"}`))
	require.True(t, IsOpenAIClientPayloadError(err), "%v", err)
	require.NotNil(t, result)
	require.Equal(t, "private-response", result.UpstreamResponseModel)
	require.Equal(t, 17, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.NotContains(t, recorder.Body.String(), "private-response")
	require.Contains(t, recorder.Body.String(), "response.failed")
	require.Contains(t, recorder.Body.String(), "upstream_error")
	require.Equal(t, 1, dialer.DialCount())
	require.False(t, svc.isOpenAIWSFallbackCooling(account.ID))
}

func TestOpenAIWSHTTPBridgeClientModelPrivacy_MultilineAndPublicSnapshot(t *testing.T) {
	sse := "event: response.completed\ndata: {\ndata: \"type\":\"response.completed\",\ndata: \"response\":{\"id\":\"resp_bridge\",\"model\":\"private-response\",\"usage\":{\"input_tokens\":19,\"output_tokens\":7},\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"private-response\"}]}]}\ndata: }\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{ID: 5912, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	SetOpenAIClientRequestedModel(c, "previous-turn-public")
	payload := []byte(`{"type":"response.create","model":"mapped-name","input":"hi"}`)
	var messages [][]byte
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "sk-test", payload, len(payload), "billing-name", "", "", "", "", 2, func(message []byte) error {
		messages = append(messages, append([]byte(nil), message...))
		return nil
	}, "current-public")
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "current-public", gjson.GetBytes(messages[0], "response.model").String())
	require.Equal(t, "private-response", gjson.GetBytes(messages[0], "response.output.0.content.0.text").String())
	require.Equal(t, "billing-name", result.Model)
	require.Equal(t, "mapped-name", result.UpstreamModel)
	require.Equal(t, "private-response", result.UpstreamResponseModel)
	require.Equal(t, 19, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
}

func TestOpenAIWSHTTPBridgeClientModelPrivacy_ErrorKeepsOriginalOpsMessage(t *testing.T) {
	body := `{"error":{"type":"invalid_request_error","code":"invalid_request_error","message":"private-upstream-model rejected this request"}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{ID: 5913, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"mapped-name","input":"hi"}`)
	var messages [][]byte
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "sk-test", payload, len(payload), "billing-name", "", "", "", "", 2, func(message []byte) error {
		messages = append(messages, append([]byte(nil), message...))
		return nil
	}, "public-name")
	require.ErrorContains(t, err, "private-upstream-model")
	require.Len(t, messages, 1)
	require.Equal(t, "error", gjson.GetBytes(messages[0], "type").String())
	require.NotContains(t, string(messages[0]), "private-upstream-model")
	opsError, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "private-upstream-model rejected this request", opsError.Message)
}

func TestOpenAIWSHTTPBridgeClientModelPrivacy_MalformedKeepsPartialUsage(t *testing.T) {
	sse := "data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_partial\",\"model\":\"private-partial\",\"usage\":{\"input_tokens\":29,\"output_tokens\":6}}}\n\ndata: {\"type\":\"response.updated\",\"response\":{\"id\":\"resp_partial\",\"model\":\"private-partial\"}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{ID: 5914, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"mapped-name","input":"hi"}`)
	var messages [][]byte
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "sk-test", payload, len(payload), "billing-name", "", "", "", "", 2, func(message []byte) error {
		messages = append(messages, append([]byte(nil), message...))
		return nil
	}, "public-name")
	require.True(t, IsOpenAIClientPayloadError(err), "%v", err)
	require.NotNil(t, result)
	require.Equal(t, "private-partial", result.UpstreamResponseModel)
	require.Equal(t, 29, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.NotEmpty(t, messages)
	for _, message := range messages {
		require.NotContains(t, string(message), "private-partial")
	}
	require.Equal(t, "response.failed", gjson.GetBytes(messages[len(messages)-1], "type").String())
}

func TestOpenAIWSIngressClientModelPrivacy_MultipleTurnsAndLateTerminal(t *testing.T) {
	terminal := func(id, model string, tokens int) []byte {
		return []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":2}}}`, id, model, tokens))
	}
	firstTerminal := terminal("resp_first", "private-first", 5)
	svc, account, dialer := newWSClientModelPrivacyService(t, [][]byte{
		firstTerminal, firstTerminal, terminal("resp_second", "private-second", 7), terminal("resp_third", "private-third", 9),
	})
	cfg := svc.cfg
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turns := make(chan *OpenAIForwardResult, 4)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{
			InitialRequestModel: "public-first",
			MapRequestModel:     func(_ int, model string) (string, error) { return "mapped-" + model, nil },
			AfterTurn:           func(_ int, result *OpenAIForwardResult, _ error) { turns <- result },
		}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"channel-first","stream":false}`)
	defer func() { _ = client.CloseNow() }()
	for i, tc := range []struct {
		request, public, billing, observed string
		input                              int
	}{
		{"", "public-first", "channel-first", "private-first", 5},
		{`{"type":"response.create","model":"public-second","previous_response_id":"resp_first","stream":false}`, "public-second", "public-second", "private-second", 7},
		{`{"type":"response.create","previous_response_id":"resp_second","stream":false}`, "public-second", "public-second", "private-third", 9},
	} {
		if i > 0 {
			writeWSClientModelPrivacyFrame(t, client, coderws.MessageText, tc.request)
		}
		if i == 1 {
			late := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
			require.Equal(t, "public-first", gjson.GetBytes(late, "response.model").String())
		}
		message := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
		require.Equal(t, tc.public, gjson.GetBytes(message, "response.model").String())
		select {
		case result := <-turns:
			require.NotNil(t, result)
			require.Equal(t, tc.billing, result.Model)
			require.Equal(t, "mapped-"+tc.billing, result.UpstreamModel)
			require.Equal(t, tc.observed, result.UpstreamResponseModel)
			require.Equal(t, tc.input, result.Usage.InputTokens)
		case <-time.After(3 * time.Second):
			t.Fatal("missing completed turn result")
		}
	}
	_ = client.Close(coderws.StatusNormalClosure, "done")
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("ingress did not close")
	}
	require.Empty(t, turns, "late terminal must not add a fourth charge")
	require.Equal(t, 1, dialer.DialCount())
	require.Len(t, dialer.conn.writes, 3)
	require.Equal(t, "resp_first", dialer.conn.writes[1]["previous_response_id"])
	require.Equal(t, "resp_second", dialer.conn.writes[2]["previous_response_id"])
}

func TestOpenAIWSIngressClientModelPrivacy_MalformedLateFrameKeepsCurrentUsage(t *testing.T) {
	svc, account, dialer := newWSClientModelPrivacyService(t, [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_first","model":"private-first","usage":{"input_tokens":1,"output_tokens":1}}}`),
		[]byte(`{"type":"response.in_progress","response":{"id":"resp_second","model":"private-second","usage":{"input_tokens":31,"output_tokens":4}}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_first","model":"private-first"}`),
	})
	svc.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	svc.cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turns := make(chan *OpenAIForwardResult, 4)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) { turns <- result }}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"public-first","stream":false}`)
	defer func() { _ = client.CloseNow() }()
	_ = readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	writeWSClientModelPrivacyFrame(t, client, coderws.MessageText, `{"type":"response.create","model":"public-second","stream":false}`)
	preamble := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-second", gjson.GetBytes(preamble, "response.model").String())
	failed := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "response.failed", gjson.GetBytes(failed, "type").String())
	require.NotContains(t, string(failed), "private-")
	select {
	case err := <-serverErr:
		require.True(t, IsOpenAIClientPayloadError(err), "%v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("invalid late response did not end ingress")
	}
	require.Len(t, turns, 2)
	<-turns
	partial := <-turns
	require.NotNil(t, partial)
	require.Equal(t, "private-second", partial.UpstreamResponseModel)
	require.Equal(t, 31, partial.Usage.InputTokens)
	require.Equal(t, 4, partial.Usage.OutputTokens)
	require.Equal(t, 1, dialer.DialCount())
	require.Len(t, dialer.conn.writes, 2, "display failure must not replay the current turn")
	require.False(t, svc.isOpenAIWSFallbackCooling(account.ID))
}

func TestOpenAIWSIngressClientModelPrivacy_MalformedRateLimitDoesNotRetryOrCooldown(t *testing.T) {
	svc, account, dialer := newWSClientModelPrivacyService(t, [][]byte{
		[]byte(`{"type":"error","response":{"id":"resp_invalid","model":"private-upstream","usage":{"input_tokens":7,"output_tokens":2}},"error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"private-upstream rate limit reached"}`),
	})
	svc.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	svc.cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
	svc.accountRepo = repo
	svc.rateLimitService = &RateLimitService{accountRepo: repo}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turns := make(chan *OpenAIForwardResult, 2)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) { turns <- result }}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"public-model","stream":false}`)
	defer func() { _ = client.CloseNow() }()
	failed := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "response.failed", gjson.GetBytes(failed, "type").String())
	require.NotContains(t, string(failed), "private-upstream")
	select {
	case err := <-serverErr:
		require.True(t, IsOpenAIClientPayloadError(err), "%v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("malformed rate limit did not end ingress")
	}
	require.Len(t, turns, 1)
	partial := <-turns
	require.NotNil(t, partial)
	require.Zero(t, partial.Usage.InputTokens, "do not invent usage from fields the ingress observer does not consume")
	require.Zero(t, partial.Usage.OutputTokens)
	require.Equal(t, 1, dialer.DialCount())
	require.Len(t, dialer.conn.writes, 1)
	require.Empty(t, repo.rateLimitCalls)
}

func readWSClientModelPrivacyFrame(t *testing.T, conn *coderws.Conn, wantType coderws.MessageType) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msgType, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, wantType, msgType)
	require.True(t, json.Valid(payload), "%s", payload)
	return payload
}

func writeWSClientModelPrivacyFrame(t *testing.T, conn *coderws.Conn, msgType coderws.MessageType, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, msgType, []byte(payload)))
}

func startWSClientModelPrivacySecondTurn(t *testing.T, cfg *config.Config) (*stagedPassthroughConn, *coderws.Conn, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	upstream := newStagedPassthroughConn()
	server, serverErr := startPassthroughLifecycleServer(t, ctx, newPassthroughLifecycleService(cfg, upstream), passthroughLifecycleAccount())
	t.Cleanup(server.Close)
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"public-first"}`)
	t.Cleanup(func() { _ = client.CloseNow() })
	_ = requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"private-first","usage":{"input_tokens":1,"output_tokens":1}}}`)
	first := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-first", gjson.GetBytes(first, "response.model").String())
	writeWSClientModelPrivacyFrame(t, client, coderws.MessageText, `{"type":"response.create","model":"public-second"}`)
	_ = requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"private-first","usage":{"input_tokens":1,"output_tokens":1}}}`)
	late := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-first", gjson.GetBytes(late, "response.model").String())
	return upstream, client, serverErr
}

func TestPassthroughClientModelPrivacy_LateTerminalDoesNotReleaseNewTurn(t *testing.T) {
	upstream, client, serverErr := startWSClientModelPrivacySecondTurn(t, passthroughLifecycleConfig())
	writeWSClientModelPrivacyFrame(t, client, coderws.MessageText, `{"type":"response.create","model":"public-third"}`)
	_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	require.Contains(t, closeErr.Reason, "overlapping response.create")
	select {
	case err := <-serverErr:
		require.ErrorContains(t, err, "overlapping response.create")
	case <-time.After(3 * time.Second):
		t.Fatal("overlapping response.create did not end the session")
	}
	select {
	case payload := <-upstream.writes:
		t.Fatalf("overlapping turn reached upstream: %s", payload)
	default:
	}
}

func TestPassthroughClientModelPrivacy_LateTerminalDoesNotCancelFirstOutputTimeout(t *testing.T) {
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 10
	_, client, serverErr := startWSClientModelPrivacySecondTurn(t, cfg)
	_, err := readPassthroughLifecycleFrame(t, client, 2500*time.Millisecond)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusGoingAway, closeErr.Code)
	require.Equal(t, "upstream produced no semantic output; please reconnect", closeErr.Reason)
	select {
	case err := <-serverErr:
		var timeoutErr *openAIWSPassthroughFirstOutputTimeoutError
		require.ErrorAs(t, err, &timeoutErr)
	case <-time.After(3 * time.Second):
		t.Fatal("late terminal left the current turn unbounded")
	}
}

func TestPassthroughClientModelPrivacy_LateTerminalDoesNotSuppressCurrentRateLimit(t *testing.T) {
	upstream, client, serverErr := startWSClientModelPrivacySecondTurn(t, passthroughLifecycleConfig())
	upstream.Send(`{"type":"error","error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"private-second rate limit reached"}}`)
	_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.Code)
	require.Equal(t, "upstream rate limit exceeded; please reconnect", closeErr.Reason)
	select {
	case err := <-serverErr:
		var clientCloseErr *OpenAIWSClientCloseError
		require.ErrorAs(t, err, &clientCloseErr)
		require.Equal(t, coderws.StatusTryAgainLater, clientCloseErr.StatusCode())
	case <-time.After(3 * time.Second):
		t.Fatal("current pre-output rate limit was hidden by a late terminal")
	}
}

func TestPassthroughClientModelPrivacy_MalformedEventKeepsSingleUsageResult(t *testing.T) {
	for _, eventType := range []string{"response.in_progress", "response.completed", "error", "response.failed"} {
		t.Run(eventType, func(t *testing.T) {
			terminal := eventType == "response.completed" || eventType == "response.failed"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			account := passthroughLifecycleAccount()
			repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
			svc.accountRepo = repo
			svc.rateLimitService = &RateLimitService{accountRepo: repo}
			type turnRecord struct {
				result *OpenAIForwardResult
				err    error
			}
			turns := make(chan turnRecord, 4)
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) { turns <- turnRecord{result, err} }}
			})
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"public-current"}`)
			defer func() { _ = client.CloseNow() }()
			_ = requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
			if eventType != "error" {
				upstream.Send(`{"type":"response.in_progress","response":{"id":"resp_current","model":"private-current","usage":{"input_tokens":21,"output_tokens":8}}}`)
				_ = readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
			}
			upstream.Send(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_current","model":"private-current","usage":{"input_tokens":21,"output_tokens":8},"error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"private-current rate limit reached"}},"error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"private-current rate limit reached"}`, eventType))
			failed := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
			require.Equal(t, "response.failed", gjson.GetBytes(failed, "type").String())
			require.NotContains(t, string(failed), "private-current")
			_, readErr := readPassthroughLifecycleFrame(t, client, 3*time.Second)
			var closeErr coderws.CloseError
			require.ErrorAs(t, readErr, &closeErr)
			require.Equal(t, coderws.StatusInternalError, closeErr.Code)
			select {
			case err := <-serverErr:
				require.True(t, IsOpenAIClientPayloadError(err), "%v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("malformed upstream payload did not end the session")
			}
			require.Len(t, turns, 1, "one upstream turn must settle exactly once")
			record := <-turns
			require.NotNil(t, record.result)
			require.Equal(t, "private-current", record.result.UpstreamResponseModel)
			require.Equal(t, 21, record.result.Usage.InputTokens)
			require.Equal(t, 8, record.result.Usage.OutputTokens)
			if terminal || eventType == "error" {
				require.NoError(t, record.err, "terminal observation already settled the result before client writing")
			} else {
				require.True(t, IsOpenAIClientPayloadError(record.err))
			}
			require.False(t, svc.isOpenAIWSFallbackCooling(passthroughLifecycleAccount().ID))
			require.Empty(t, repo.rateLimitCalls, "malformed protocol frames must not trigger account cooldown")
			select {
			case payload := <-upstream.writes:
				t.Fatalf("display failure replayed upstream request: %s", payload)
			default:
			}
		})
	}
}

func TestOpenAIWSClientModelPrivacy_PolicyBlockHidesConfiguredModelName(t *testing.T) {
	for _, tc := range []struct {
		name              string
		followup, ingress bool
	}{
		{"passthrough_first", false, false}, {"passthrough_followup", true, false}, {"ingress_first", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			svc.settingService = newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
				ServiceTier: OpenAIFastTierPriority, Action: BetaPolicyActionBlock, Scope: BetaPolicyScopeAll,
				ErrorMessage: "private-upstream-model is blocked", FallbackAction: BetaPolicyActionPass,
			}}}).settingService
			account := passthroughLifecycleAccount()
			if tc.ingress {
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
			}
			server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
			defer server.Close()
			first := `{"type":"response.create","model":"public-model","service_tier":"priority"}`
			if tc.followup {
				first = `{"type":"response.create","model":"public-model"}`
			}
			client := dialPassthroughLifecycleClientWithPayload(t, server, first)
			defer func() { _ = client.CloseNow() }()
			if tc.followup {
				_ = requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
				upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"private-upstream-model","usage":{"input_tokens":1,"output_tokens":1}}}`)
				_ = readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
				writeWSClientModelPrivacyFrame(t, client, coderws.MessageText, `{"type":"response.create","model":"public-model","service_tier":"priority"}`)
			}
			blocked := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
			require.Equal(t, "policy_violation", gjson.GetBytes(blocked, "error.code").String())
			require.Equal(t, "Request blocked by policy.", gjson.GetBytes(blocked, "error.message").String())
			require.NotContains(t, string(blocked), "private-upstream-model")
			_, _ = readPassthroughLifecycleFrame(t, client, 3*time.Second)
			select {
			case err := <-serverErr:
				var closeErr *OpenAIWSClientCloseError
				require.ErrorAs(t, err, &closeErr)
				require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
				require.Equal(t, "Request blocked by policy.", closeErr.Reason())
				var original *OpenAIFastBlockedError
				require.ErrorAs(t, err, &original)
				require.Equal(t, "private-upstream-model is blocked", original.Message)
			case <-time.After(3 * time.Second):
				t.Fatal("policy block did not end the session")
			}
			require.Empty(t, upstream.writes, "blocked request must not be sent upstream")
		})
	}
}

func TestPassthroughClientModelPrivacy_SessionTurnsLateEventsAndBinary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 10
	turns := make(chan *OpenAIForwardResult, 8)
	contextReady := make(chan *gin.Context, 1)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, newPassthroughLifecycleService(cfg, upstream), passthroughLifecycleAccount(), func(c *gin.Context) *OpenAIWSIngressHooks {
		contextReady <- c
		return &OpenAIWSIngressHooks{
			InitialRequestModel: "public-first",
			MapRequestModel:     func(_ int, model string) (string, error) { return "mapped-" + model, nil },
			AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
				if result != nil && err == nil {
					turns <- result
				}
			},
		}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"channel-first"}`)
	defer func() { _ = client.CloseNow() }()
	gatewayContext := <-contextReady
	first := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	require.Equal(t, "mapped-public-first", gjson.GetBytes(first, "model").String())
	upstream.Send(`{"type":"response.created","model":"private-root","response":{"id":"resp_first","model":"private-first"}}`)
	created := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-first", gjson.GetBytes(created, "model").String())
	require.Equal(t, "public-first", gjson.GetBytes(created, "response.model").String())
	writeWSClientModelPrivacyFrame(t, client, coderws.MessageText, `{"type":"session.update","session":{"model":"public-session"}}`)
	_ = requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	upstream.Send(`{"type":"session.updated","session":{"model":"private-session"}}`)
	session := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-session", gjson.GetBytes(session, "session.model").String())
	firstTerminal := `{"type":"response.completed","response":{"id":"resp_first","model":"private-first","usage":{"input_tokens":11,"output_tokens":3}}}`
	upstream.Send(firstTerminal)
	completed := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-first", gjson.GetBytes(completed, "response.model").String())
	firstResult := <-turns
	require.Equal(t, "public-first", firstResult.Model)
	require.Equal(t, "mapped-public-first", firstResult.UpstreamModel)
	require.Equal(t, "private-first", firstResult.UpstreamResponseModel)
	require.Equal(t, 11, firstResult.Usage.InputTokens)
	writeWSClientModelPrivacyFrame(t, client, coderws.MessageText, `{"type":"response.create"}`)
	second := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	require.Equal(t, "mapped-public-session", gjson.GetBytes(second, "model").String())
	upstream.Send(firstTerminal)
	late := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-first", gjson.GetBytes(late, "response.model").String())
	upstream.Send(`{"type":"response.failed","response":{"id":"resp_first","model":"private-first","error":{"code":"invalid_request_error","message":"private-first failed"}}}`)
	lateFailure := readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-first", gjson.GetBytes(lateFailure, "response.model").String())
	require.NotContains(t, string(lateFailure), "private-first")
	_, failed := GetOpsStreamError(gatewayContext)
	require.False(t, failed, "a late failure must not mark the current turn as failed")
	select {
	case duplicate := <-turns:
		t.Fatalf("late terminal was billed twice: %+v", duplicate)
	default:
	}
	upstream.Send(`{"type":"response.created","response":{"id":"resp_second","model":"private-second"}}`)
	created = readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-session", gjson.GetBytes(created, "response.model").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_second","model":"private-second","usage":{"input_tokens":13,"output_tokens":5}}}`)
	completed = readWSClientModelPrivacyFrame(t, client, coderws.MessageText)
	require.Equal(t, "public-session", gjson.GetBytes(completed, "response.model").String())
	secondResult := <-turns
	require.Equal(t, "public-session", secondResult.Model)
	require.Equal(t, "mapped-public-session", secondResult.UpstreamModel)
	require.Equal(t, "private-second", secondResult.UpstreamResponseModel)
	require.Equal(t, 13, secondResult.Usage.InputTokens)
	require.Equal(t, 5, secondResult.Usage.OutputTokens)
	writeWSClientModelPrivacyFrame(t, client, coderws.MessageBinary, `{"type":"response.create","model":"public-binary"}`)
	third := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	require.Equal(t, "mapped-public-binary", gjson.GetBytes(third, "model").String())
	upstream.frames <- stagedPassthroughFrame{messageType: coderws.MessageBinary, payload: []byte(`{"type":"response.completed","response":{"id":"resp_binary","model":"private-binary","output":[]}}`)}
	completed = readWSClientModelPrivacyFrame(t, client, coderws.MessageBinary)
	require.Equal(t, "public-binary", gjson.GetBytes(completed, "response.model").String())
	upstream.Fail(io.EOF)
	_ = client.Close(coderws.StatusNormalClosure, "done")
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("completed binary turn did not finish")
	}
}
