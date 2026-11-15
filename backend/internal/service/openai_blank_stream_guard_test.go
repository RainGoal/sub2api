package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newBlankStreamGuardTest(t *testing.T, ctx context.Context, body io.ReadCloser) (*openAIBlankStreamGuard, *OpenAIGatewayService, *gin.Context, *Account) {
	t.Helper()
	request := []byte(`{"model":"gpt-5","stream":true}`)
	svc := configureCollaborationTestService(t, nil, true)
	settings := enabledCollaborationTestSettings()
	settings.BlankStreamFailoverEnabled = true
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	c := newCollaborationTestContext(request, 11)
	account := newOpenAIRejectedFieldTestAccount()
	svc.beginBlankStreamPolicy(ctx, c, account, request, false)
	if body == nil {
		body = io.NopCloser(strings.NewReader(""))
	}
	g := svc.newBlankStreamGuard(ctx, c, account, &http.Response{Body: body}, time.Now(), "")
	require.NotNil(t, g)
	t.Cleanup(g.Close)
	return g, svc, c, account
}

func observeBlankStreamPrelude(t *testing.T, g *openAIBlankStreamGuard, stream string) {
	t.Helper()
	for _, frame := range strings.Split(strings.TrimSpace(stream), "\n\n") {
		kind, payload := collaborationSSEEvent([]byte(frame + "\n\n"))
		require.True(t, g.Observe(payload, kind), string(payload))
		require.False(t, g.BlocksFailover(), string(payload))
	}
}

func TestOpenAIBlankStreamGuardAcceptsOnlyVerifiedBlankFailure(t *testing.T) {
	blank := `[{"id":"msg_placeholder","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":" ","annotations":[]}]}]`
	for _, output := range []string{"", `,"output":[]`, `,"output":` + blank} {
		t.Run(output, func(t *testing.T) {
			g, _, _, _ := newBlankStreamGuardTest(t, context.Background(), nil)
			observeBlankStreamPrelude(t, g, collaborationProbeWhitespacePrelude)
			failed := []byte(`{"type":"response.failed","response":{"id":"resp_before","status":"failed","error":{"message":"Upstream request failed"}` + output + `}}`)
			require.False(t, g.Observe(failed, "response.failed"))
			require.False(t, g.BlocksFailover())
			require.NoError(t, g.Err())
		})
	}
}

func TestOpenAIBlankStreamGuardLocksOnOutputOrUncertainFailure(t *testing.T) {
	cases := map[string]string{
		"text":                   `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":"hello"}`,
		"unicode blank":          `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":"\u00a0"}`,
		"unknown event":          `{"type":"response.future_event"}`,
		"reasoning":              `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","summary":[]}}`,
		"tool":                   `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","arguments":""}}`,
		"unknown field":          `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":" ","audio":"result"}`,
		"duplicate delta":        `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":" ","delta":"result"}`,
		"identity conflict":      `{"type":"response.failed","response":{"id":"resp_other"}}`,
		"terminal tool":          `{"type":"response.failed","response":{"id":"resp_before","output":[{"type":"function_call","arguments":"{}"}]}}`,
		"terminal text":          `{"type":"response.failed","response":{"id":"resp_before","output":[{"id":"msg_placeholder","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}}`,
		"duplicate output":       `{"type":"response.failed","response":{"id":"resp_before","output":[],"output":[{"type":"function_call"}]}}`,
		"error output":           `{"type":"error","output":[{"type":"function_call"}]}`,
		"unknown terminal field": `{"type":"response.failed","tool_call":{"name":"run"},"response":{"id":"resp_before","output":[]}}`,
		"unknown response field": `{"type":"response.failed","response":{"id":"resp_before","output":[],"executed_tool":{"name":"run"}}}`,
		"unknown error field":    `{"type":"error","error":{"message":"failed","tool_call":{"name":"run"}}}`,
		"success":                `{"type":"response.completed","response":{"id":"resp_before","output":[]}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			g, _, _, _ := newBlankStreamGuardTest(t, context.Background(), nil)
			end := strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.done"`)
			observeBlankStreamPrelude(t, g, collaborationProbeWhitespacePrelude[:end])
			require.False(t, g.Observe([]byte(payload), gjson.Get(payload, "type").String()))
			require.True(t, g.BlocksFailover())
			require.False(t, g.Observe([]byte(`{"type":"response.failed"}`), "response.failed"))
			require.True(t, g.BlocksFailover(), "later failures must never reopen replay")
			require.NoError(t, g.Err())
		})
	}
}

func TestOpenAIBlankStreamGuardRejectsOrphanAndAmbiguousBlank(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.output_text.delta","delta":" "}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message","role":"assistant","content":[],"future":"value"}}`,
		`{"type":"response.created","response":{"id":"resp","output":[{"type":"function_call"}]}}`,
		`{"type":"response.created","type":"response.output_text.delta","response":{"id":"resp"}}`,
		`{"type":"response.metadata","tool_call":{"name":"run"}}`,
		`not-json`,
	} {
		g, _, _, _ := newBlankStreamGuardTest(t, context.Background(), nil)
		require.False(t, g.Observe([]byte(payload), gjson.Get(payload, "type").String()), payload)
		require.True(t, g.BlocksFailover(), payload)
	}
}

func TestOpenAIBlankStreamGuardDeadlineIsAbsoluteAndBounded(t *testing.T) {
	g, _, _, _ := newBlankStreamGuardTest(t, context.Background(), nil)
	observeBlankStreamPrelude(t, g, collaborationProbeCreated)
	require.True(t, g.deadline.IsZero(), "metadata does not start the blank watchdog")
	end := strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.done"`)
	observeBlankStreamPrelude(t, g, collaborationProbeWhitespacePrelude[len(collaborationProbeCreated):end])
	deadline := g.deadline
	require.WithinDuration(t, time.Now().Add(openAIBlankStreamMaxWait), deadline, time.Second)
	for i := 0; i < 5; i++ {
		require.True(t, g.Observe([]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":" \t\r\n"}`), "response.output_text.delta"))
		require.True(t, g.Observe([]byte(`{"type":"keepalive"}`), "keepalive"))
		require.Equal(t, deadline, g.deadline)
	}
	g2, svc, c, account := newBlankStreamGuardTest(t, context.Background(), nil)
	g2.Close()
	svc.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 30
	start := time.Now().Add(-10 * time.Second)
	g2 = svc.newBlankStreamGuard(context.Background(), c, account, &http.Response{Body: io.NopCloser(strings.NewReader(""))}, start, "")
	defer g2.Close()
	observeBlankStreamPrelude(t, g2, collaborationProbeWhitespacePrelude[:end])
	require.Equal(t, start.Add(30*time.Second), g2.deadline)
}

func TestOpenAIBlankStreamGuardTracksActualWhitespaceNotEmptyStructure(t *testing.T) {
	g, _, _, _ := newBlankStreamGuardTest(t, context.Background(), nil)
	end := strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.delta"`)
	observeBlankStreamPrelude(t, g, collaborationProbeWhitespacePrelude[:end])
	require.False(t, g.HasBlankText())
	require.True(t, g.Observe([]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":""}`), "response.output_text.delta"))
	require.False(t, g.HasBlankText())
	require.True(t, g.Observe([]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":" \t\r\n"}`), "response.output_text.delta"))
	require.True(t, g.HasBlankText())
	var disabled *openAIBlankStreamGuard
	require.False(t, disabled.HasBlankText())
}

func TestOpenAIBlankStreamTimeoutUsesFirstOutputRetryBudget(t *testing.T) {
	for _, responseDeadline := range []bool{false, true} {
		c := newCollaborationTestContext([]byte(`{}`), 11)
		start := time.Now()
		guard := &openAIBlankStreamGuard{wait: openAIBlankStreamMaxWait, reasoning: "high"}
		wantDetail := "timeout_ms=30000"
		if responseDeadline {
			guard.responseEnd = start.Add(15 * time.Second)
			guard.deadline = guard.responseEnd
			wantDetail = "timeout_ms=15000"
		}
		svc := &OpenAIGatewayService{}
		err := svc.newBlankStreamTimeoutError(context.Background(), c, &Account{ID: 1, Platform: PlatformOpenAI}, guard, start, "gpt-5", http.Header{"X-Request-Id": []string{"req-blank"}})
		require.Equal(t, http.StatusGatewayTimeout, err.StatusCode)
		require.True(t, err.SafeToFailoverAfterWrite)
		raw, ok := c.Get(OpsUpstreamErrorsKey)
		require.True(t, ok)
		events, ok := raw.([]*OpsUpstreamErrorEvent)
		require.True(t, ok)
		require.Len(t, events, 1)
		require.Equal(t, "first_output_timeout", events[0].Kind)
		require.Equal(t, "req-blank", events[0].UpstreamRequestID)
		require.Contains(t, events[0].Detail, "phase=blank_prelude")
		require.Contains(t, events[0].Detail, wantDetail)
	}
}

func TestOpenAIBlankStreamGuardWatchdogInterruptsBlockedScanner(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	g, _, _, _ := newBlankStreamGuardTest(t, context.Background(), reader)
	g.wait = 20 * time.Millisecond
	observeBlankStreamPrelude(t, g, collaborationProbeWhitespacePrelude)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Scan()
		done <- scanner.Err()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blank deadline did not interrupt the upstream read")
	}
	require.ErrorIs(t, g.Err(), errOpenAIBlankStreamTimeout)
	require.False(t, g.BlocksFailover())
	g.Close()
	require.ErrorIs(t, g.Err(), errOpenAIBlankStreamTimeout, "EOF/cleanup must retain the deadline cause")
}

func TestOpenAIBlankStreamGuardCancelNeverAllowsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	body := &firstOutputCloseTrackingBody{ReadCloser: reader, closed: make(chan struct{})}
	g, _, _, _ := newBlankStreamGuardTest(t, ctx, body)
	observeBlankStreamPrelude(t, g, collaborationProbeWhitespacePrelude)
	cancel()
	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release the upstream reader")
	}
	require.ErrorIs(t, g.Err(), context.Canceled)
	require.False(t, errors.Is(g.Err(), errOpenAIBlankStreamTimeout))
	require.True(t, g.BlocksFailover())
}

func TestOpenAIBlankStreamGuardRealOutputDisarmsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &firstOutputCloseTrackingBody{ReadCloser: io.NopCloser(strings.NewReader("")), closed: make(chan struct{})}
	g, _, _, _ := newBlankStreamGuardTest(t, ctx, body)
	g.wait = 20 * time.Millisecond
	end := strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.done"`)
	observeBlankStreamPrelude(t, g, collaborationProbeWhitespacePrelude[:end])
	require.False(t, g.Observe([]byte(`{"type":"response.output_text.delta","item_id":"msg_placeholder","output_index":0,"content_index":0,"delta":"ready"}`), "response.output_text.delta"))
	require.True(t, g.BlocksFailover())
	cancel()
	require.NoError(t, g.Err(), "post-output cancellation belongs to the existing usage-drain path")
	select {
	case <-body.closed:
		t.Fatal("blank watchdog closed a stream after real output")
	case <-time.After(60 * time.Millisecond):
	}
	require.NoError(t, g.Err())
}

func TestOpenAIBlankStreamPolicySnapshotsPerRequestAndChecksScope(t *testing.T) {
	g, svc, c, account := newBlankStreamGuardTest(t, context.Background(), nil)
	g.Close()
	request := []byte(`{"model":"gpt-5","stream":true}`)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
	settings := enabledCollaborationTestSettings()
	settings.BlankStreamFailoverEnabled = false
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	svc.beginBlankStreamPolicy(context.Background(), c, account, request, false)
	retry := svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), "")
	require.NotNil(t, retry, "an account retry uses the existing request snapshot")
	retry.Close()
	key := getAPIKeyFromContext(c)
	otherGroup := int64(999)
	originalGroup := key.GroupID
	key.GroupID = &otherGroup
	require.Nil(t, svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), ""))
	key.GroupID = originalGroup
	key.ID++
	require.Nil(t, svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), ""))
	key.ID--
	svc.beginBlankStreamPolicy(context.Background(), c, account, request, true)
	require.Nil(t, svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), ""), "next WS turn reads the saved shutdown")
	settings.BlankStreamFailoverEnabled = true
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	svc.beginBlankStreamPolicy(context.Background(), c, account, request, false)
	require.Nil(t, svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), ""), "disabled snapshots also survive retries")
}

func TestOpenAIBlankStreamPolicyRejectsHostedOrUnknownTools(t *testing.T) {
	_, svc, c, account := newBlankStreamGuardTest(t, context.Background(), nil)
	for _, request := range []string{
		`{"tools":[{"type":"web_search"}]}`,
		`{"tools":[{"type":"future_tool"}]}`,
		`{"tools":[{"type":"namespace","tools":[{"type":"mcp"}]}]}`,
		`{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]}]}`,
		`invalid`,
	} {
		svc.beginBlankStreamPolicy(context.Background(), c, account, []byte(request), true)
		require.Nil(t, svc.newBlankStreamGuard(context.Background(), c, account, &http.Response{Body: io.NopCloser(strings.NewReader(""))}, time.Now(), ""), request)
	}
	var disabled *openAIBlankStreamGuard
	require.False(t, disabled.Observe([]byte(`{"type":"response.failed"}`), "response.failed"))
	require.False(t, disabled.BlocksFailover())
	require.NoError(t, disabled.Err())
	disabled.Close()
}

func TestOpenAIBlankStreamPolicyFinalBodyOnlyNarrowsReplay(t *testing.T) {
	for _, finalBody := range []string{
		`{"tools":[{"type":"function","name":"run"},{"type":"web_search"}]}`,
		`{"tools":[{"type":"future_tool"}]}`,
		`invalid`,
	} {
		_, svc, c, account := newBlankStreamGuardTest(t, context.Background(), nil)
		request := []byte(`{"tools":[{"type":"function","name":"run"}]}`)
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
		svc.beginBlankStreamPolicy(context.Background(), c, account, request, true)
		noteBlankStreamUpstreamBody(c, request)
		guard := svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), "")
		require.NotNil(t, guard, "client functions remain eligible")
		guard.Close()
		noteBlankStreamUpstreamBody(c, []byte(finalBody))
		require.Nil(t, svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), ""), finalBody)
		noteBlankStreamUpstreamBody(c, request)
		svc.beginBlankStreamPolicy(context.Background(), c, account, request, false)
		require.Nil(t, svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), ""), "later safe bodies cannot reopen this request")
		svc.beginBlankStreamPolicy(context.Background(), c, account, request, true)
		next := svc.newBlankStreamGuard(context.Background(), c, account, resp, time.Now(), "")
		require.NotNil(t, next, "the next logical request takes a fresh snapshot")
		next.Close()
	}
	noteBlankStreamUpstreamBody(nil, []byte(`invalid`))
	c := newCollaborationTestContext([]byte(`{}`), 11)
	noteBlankStreamUpstreamBody(c, []byte(`invalid`))
	_, exists := c.Get(openAIBlankStreamPolicyKey)
	require.False(t, exists, "checking a body must not create or enable policy")
}
