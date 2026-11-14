package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const collaborationMixedRequest = `{"model":"gpt-5","input":[{"type":"agent_message","id":"amsg_old","author":"/root","recipient":"/root/child","content":[{"type":"input_text","text":"Payload:\n"},{"type":"encrypted_content","encrypted_content":"old-opaque"}]},{"type":"message","role":"user","content":"Complete the new independent task: report NEW_TASK_OK"}]}`
const collaborationFailedJSON = `{"error":{"code":"invalid_encrypted_content","message":"Unable to decrypt amsg_old"}}`
const collaborationCompletedJSON = `{"id":"resp_ok","object":"response","status":"completed","model":"gpt-5","output":[{"id":"msg_ok","type":"message","role":"assistant","content":[{"type":"output_text","text":"NEW_TASK_OK","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":2}}`

func newCollaborationTestContext(body []byte, keyID int64) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("collaboration_test_recorder", recorder)
	groupID := int64(7)
	c.Set("api_key", &APIKey{ID: keyID, UserID: 9, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI}})
	c.Request.Header.Set("session-id", "collaboration-session")
	c.Request.Header.Set("thread-id", "child-thread")
	return c
}

func configureCollaborationTestService(t *testing.T, upstream *httpUpstreamRecorder, enabled bool) *OpenAIGatewayService {
	t.Helper()
	svc := newOpenAIRejectedFieldTestService(upstream)
	svc.settingService = NewSettingService(&panelRateLimitSettingRepo{}, svc.cfg)
	settings := enabledCollaborationTestSettings()
	settings.Enabled = enabled
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	return svc
}

func collaborationTestResponse(status int, payload string, stream bool) *http.Response {
	resp := newOpenAIRejectedFieldTestResponse(status, payload)
	if stream {
		resp.Header.Set("Content-Type", "text/event-stream")
	}
	return resp
}

func TestCollaborationForwardRecoversOnceAndRemembersOnlyCompleted(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%v/stream=%v", passthrough, stream), func(t *testing.T) {
				body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, fmt.Sprintf(`"model":"gpt-5","stream":%v`, stream), 1))
				failure, status := collaborationFailedJSON, http.StatusBadRequest
				success := collaborationCompletedJSON
				if stream {
					status = http.StatusOK
					failure = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bad\",\"output\":[]}}\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_bad\",\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"message\":\"Unable to decrypt amsg_old\"}}}\n\n"
					success = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + collaborationCompletedJSON + "}\n\n"
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					collaborationTestResponse(status, failure, stream), collaborationTestResponse(200, success, stream), collaborationTestResponse(200, success, stream),
				}}
				svc := configureCollaborationTestService(t, upstream, true)
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = passthrough
				c := newCollaborationTestContext(body, 11)
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 2)
				require.Contains(t, string(upstream.bodies[0]), "old-opaque")
				require.NotContains(t, string(upstream.bodies[1]), "old-opaque")
				require.Contains(t, string(upstream.bodies[1]), "NEW_TASK_OK")
				require.Contains(t, string(upstream.bodies[1]), "Gateway compatibility notice")
				recorder, _ := c.Get("collaboration_test_recorder")
				recorded, ok := recorder.(*httptest.ResponseRecorder)
				require.True(t, ok)
				require.NotContains(t, recorded.Body.String(), "resp_bad")
				require.Equal(t, 10, result.Usage.InputTokens)
				_, err = svc.Forward(context.Background(), newCollaborationTestContext(body, 11), account, body)
				require.NoError(t, err)
				require.Len(t, upstream.bodies, 3)
				require.NotContains(t, string(upstream.bodies[2]), "old-opaque")
			})
		}
	}
}

func TestCollaborationOffAndOtherKeysPreserveCipher(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		key     int64
	}{{"off", false, 11}, {"other-key", true, 12}} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(collaborationMixedRequest)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(400, collaborationFailedJSON, false)}}
			svc := configureCollaborationTestService(t, upstream, tc.enabled)
			_, err := svc.Forward(context.Background(), newCollaborationTestContext(body, tc.key), newOpenAIRejectedFieldTestAccount(), body)
			require.Error(t, err)
			require.Len(t, upstream.bodies, 1)
			require.True(t, gjson.GetBytes(upstream.bodies[0], "input.0.content.1.encrypted_content").Exists())
		})
	}
}

func TestCollaborationHTTPBridgeRecoversAndHonorsShutdownNextTurn(t *testing.T) {
	body := []byte(collaborationMixedRequest)
	success := "data: {\"type\":\"response.completed\",\"response\":" + collaborationCompletedJSON + "}\n\n"
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(400, collaborationFailedJSON, false), collaborationTestResponse(200, success, true),
		collaborationTestResponse(400, collaborationFailedJSON, false),
	}}
	svc := configureCollaborationTestService(t, upstream, true)
	c := newCollaborationTestContext(body, 11)
	var events []string
	write := func(event []byte) error { events = append(events, string(event)); return nil }
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, newOpenAIRejectedFieldTestAccount(), "test-token", body, len(body), "gpt-5", "", "", "", "", 1, write)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.NotContains(t, strings.Join(events, ""), "invalid_encrypted_content")
	settings := enabledCollaborationTestSettings()
	settings.Enabled = false
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	_, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, newOpenAIRejectedFieldTestAccount(), "test-token", body, len(body), "gpt-5", "", "", "", "", 2, write)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 3)
	require.Contains(t, string(upstream.bodies[2]), "old-opaque")
}

func TestCollaborationRecoveryFailureDoesNotRetryOrRemember(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			body := []byte(strings.Replace(collaborationMixedRequest, `"role":"user","content"`, `"role":"user","status":"completed","content"`, 1))
			secondFailure := `{"error":{"code":"unknown_parameter","message":"Unknown parameter: input[1].status.","param":"input[1].status"}}`
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				collaborationTestResponse(400, collaborationFailedJSON, false), collaborationTestResponse(400, secondFailure, false),
			}}
			svc := configureCollaborationTestService(t, upstream, true)
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_passthrough"] = passthrough
			c := newCollaborationTestContext(body, 11)
			_, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Len(t, upstream.bodies, 2)
			a := collaborationAttempt(c)
			require.True(t, a.stopFailover)
			require.Empty(t, svc.collaborationCache.read(a.scope+":"+a.branch).omissions)
		})
	}
}

func TestCollaborationEarlierFieldRetryConsumesRecoveryBudget(t *testing.T) {
	body := []byte(strings.Replace(collaborationMixedRequest, `"role":"user","content"`, `"role":"user","status":"completed","content"`, 1))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(400, `{"error":{"code":"unknown_parameter","message":"Unknown parameter: input[1].status.","param":"input[1].status"}}`, false),
		collaborationTestResponse(400, collaborationFailedJSON, false),
	}}
	svc := configureCollaborationTestService(t, upstream, true)
	_, err := svc.Forward(context.Background(), newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, string(upstream.bodies[1]), "old-opaque")
}

func TestCollaborationExplicitReasoningFailureKeepsAgentCipher(t *testing.T) {
	body := []byte(strings.TrimSuffix(collaborationMixedRequest, "]}") + `,{"type":"reasoning","encrypted_content":"reasoning-cipher"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(400, `{"error":{"code":"invalid_encrypted_content","message":"Invalid reasoning encrypted content","param":"input[2].encrypted_content"}}`, false),
		collaborationTestResponse(200, collaborationCompletedJSON, false),
	}}
	svc := configureCollaborationTestService(t, upstream, true)
	_, err := svc.Forward(context.Background(), newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, string(upstream.bodies[1]), "old-opaque")
	require.NotContains(t, string(upstream.bodies[1]), "reasoning-cipher")
}

func TestCollaborationHostedToolsAndPartialOutputNeverReplay(t *testing.T) {
	for _, tc := range []struct{ name, tools, prefix string }{
		{"hosted", `,"tools":[{"type":"web_search"}]`, ""},
		{"partial", "", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"started\"}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, `"model":"gpt-5","stream":true`+tc.tools, 1))
			failure := tc.prefix + "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_bad\",\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"message\":\"Unable to decrypt amsg_old\"}}}\n\n"
			upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, failure, true)}}
			svc := configureCollaborationTestService(t, upstream, true)
			_, err := svc.Forward(context.Background(), newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body)
			require.Error(t, err)
			require.Len(t, upstream.bodies, 1)
		})
	}
}

func TestCollaborationJSONBusinessFailureIsNotSuccess(t *testing.T) {
	body := []byte(collaborationMixedRequest)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(400, collaborationFailedJSON, false),
		collaborationTestResponse(200, `{"id":"resp_bad","status":"failed","error":{"code":"invalid_request_error","message":"Still invalid"},"output":[]}`, false),
	}}
	svc := configureCollaborationTestService(t, upstream, true)
	c := newCollaborationTestContext(body, 11)
	_, err := svc.Forward(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 2)
	a := collaborationAttempt(c)
	require.False(t, a.completed)
	require.Empty(t, svc.collaborationCache.read(a.scope+":"+a.branch).omissions)
}

func TestCollaborationBridgeSameTurnDoesNotResetBudget(t *testing.T) {
	body := []byte(collaborationMixedRequest)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(429, `{"error":{"code":"rate_limit_exceeded","message":"Too many requests"}}`, false),
		collaborationTestResponse(400, collaborationFailedJSON, false),
	}}
	svc := configureCollaborationTestService(t, upstream, true)
	c := newCollaborationTestContext(body, 11)
	write := func([]byte) error { return nil }
	for i := 0; i < 2; i++ {
		_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, newOpenAIRejectedFieldTestAccount(), "test-token", body, len(body), "gpt-5", "", "", "", "", 1, write)
		require.Error(t, err)
	}
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, 2, collaborationPriorUpstreamAttempts(c))
	require.Contains(t, string(upstream.bodies[1]), "old-opaque")
}

func TestCollaborationHostedToolDeclarations(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"tools":[{"type":"namespace","tools":[{"type":"function"}]}]}`, false},
		{`{"tools":[{"type":"namespace","children":[{"type":"mcp"}]}]}`, true},
		{`{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]}]}`, true},
		{`{"tools":[{"type":"future_hosted_tool"}]}`, true},
	} {
		require.Equal(t, tc.want, collaborationHasHostedTools([]byte(tc.body)), tc.body)
	}
	require.True(t, collaborationHasPlaintextAgentMessage([]byte(`{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"complete task"}]}]}`)))
	require.False(t, collaborationHasPlaintextAgentMessage([]byte(collaborationMixedRequest)))
}

func TestCollaborationRetryRechecksCancellationAndShutdown(t *testing.T) {
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	c := newCollaborationTestContext(body, 11)
	account := newOpenAIRejectedFieldTestAccount()
	a := svc.beginCollaborationAttempt(context.Background(), c, account, body, false)
	a.recoveryUsed = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, svc.collaborationRetryError(ctx, c, account), context.Canceled)
	settings := enabledCollaborationTestSettings()
	settings.Enabled = false
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	require.Error(t, svc.collaborationRetryError(context.Background(), c, account))
}
