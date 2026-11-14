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
)

// Matches the provider's failed, blank-only response captured in production;
// identifiers are synthetic and no user request or ciphertext is retained.
func collaborationProviderWhitespaceFailure() string {
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_placeholder","created_at":1794667325,"model":"gpt-5","object":"response","output":[],"status":"in_progress"},"sequence_number":0}`,
		`{"type":"response.in_progress","response":{"id":"resp_placeholder","created_at":1794667325,"model":"gpt-5","object":"response","output":[],"status":"in_progress"},"sequence_number":1}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"content":[],"id":"msg_placeholder","role":"assistant","status":"in_progress","type":"message"},"sequence_number":2}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg_placeholder","part":{"annotations":[],"text":"","type":"output_text"},"sequence_number":3}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":" ","sequence_number":4}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"msg_placeholder","text":" ","sequence_number":5}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg_placeholder","part":{"annotations":[],"text":" ","type":"output_text"},"sequence_number":6}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"content":[{"annotations":[],"text":" ","type":"output_text"}],"id":"msg_placeholder","role":"assistant","status":"completed","type":"message"},"sequence_number":7}`,
		`{"type":"response.failed","response":{"id":"resp_placeholder","created_at":1794667325,"model":"gpt-5","object":"response","status":"failed","error":{"code":"invalid_encrypted_content","message":"Encrypted content could not be verified.","status_code":400,"type":"invalid_request_error"}},"sequence_number":8}`,
	}
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func collaborationProviderFailureCases() []struct {
	name, body string
	status     int
	stream     bool
} {
	return []struct {
		name, body string
		status     int
		stream     bool
	}{
		{"unmapped_id", `{"error":{"code":"invalid_encrypted_content","message":"Unable to decrypt amsg_upstream_rewritten"}}`, http.StatusBadRequest, false},
		{"blank_placeholder", collaborationProviderWhitespaceFailure(), http.StatusOK, true},
	}
}

func forwardCollaborationProviderTest(t *testing.T, svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte, route string, turn int) (*OpenAIForwardResult, string, error) {
	t.Helper()
	if route == "bridge" {
		var events []string
		write := func(event []byte) error { events = append(events, string(event)); return nil }
		result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "test-token", body, len(body), "gpt-5", "", "", "", "", turn, write)
		return result, strings.Join(events, "\n"), err
	}
	result, err := svc.Forward(context.Background(), c, account, body)
	recorder, _ := c.Get("collaboration_test_recorder")
	recorded, ok := recorder.(*httptest.ResponseRecorder)
	require.True(t, ok)
	return result, recorded.Body.String(), err
}

func TestCollaborationProviderRecoveryRemembersOriginalCipherAndHonorsShutdown(t *testing.T) {
	for _, route := range []string{"forward", "passthrough", "bridge"} {
		for _, failure := range collaborationProviderFailureCases() {
			t.Run(route+"/"+failure.name, func(t *testing.T) {
				stream := failure.stream || route == "bridge"
				body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, fmt.Sprintf(`"model":"gpt-5","stream":%v`, stream), 1))
				success := collaborationCompletedJSON
				if stream {
					success = "data: {\"type\":\"response.completed\",\"response\":" + success + "}\n\n"
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					collaborationTestResponse(failure.status, failure.body, failure.stream),
					collaborationTestResponse(200, success, stream), collaborationTestResponse(200, success, stream),
					collaborationTestResponse(failure.status, failure.body, failure.stream),
				}}
				svc := configureCollaborationTestService(t, upstream, true)
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = route == "passthrough"
				c := newCollaborationTestContext(body, 11)
				result, output, err := forwardCollaborationProviderTest(t, svc, c, account, body, route, 1)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 2)
				require.Contains(t, string(upstream.bodies[0]), "old-opaque")
				require.NotContains(t, string(upstream.bodies[1]), "old-opaque")
				require.Contains(t, string(upstream.bodies[1]), "NEW_TASK_OK")
				require.Contains(t, string(upstream.bodies[1]), "Gateway compatibility notice")
				require.Contains(t, output, "NEW_TASK_OK")
				require.NotContains(t, output, "invalid_encrypted_content")
				require.NotContains(t, output, "resp_placeholder")
				require.NotContains(t, output, "msg_placeholder")
				a := collaborationAttempt(c)
				require.NotNil(t, a)
				require.Equal(t, map[string]bool{collaborationOmissionKey("amsg_old", collaborationHash("old-opaque")): true}, svc.collaborationCache.read(a.scope+":"+a.branch).omissions)
				if route != "bridge" {
					c = newCollaborationTestContext(body, 11)
				}
				_, _, err = forwardCollaborationProviderTest(t, svc, c, account, body, route, 2)
				require.NoError(t, err)
				require.Len(t, upstream.bodies, 3)
				require.NotContains(t, string(upstream.bodies[2]), "old-opaque")
				require.Contains(t, string(upstream.bodies[2]), "NEW_TASK_OK")
				settings := enabledCollaborationTestSettings()
				settings.Enabled = false
				require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
				if route != "bridge" {
					c = newCollaborationTestContext(body, 11)
				}
				_, _, err = forwardCollaborationProviderTest(t, svc, c, account, body, route, 3)
				require.Error(t, err)
				require.Len(t, upstream.bodies, 4, "shutdown must disable both cached omission and recovery retry")
				require.Contains(t, string(upstream.bodies[3]), "old-opaque")
			})
		}
	}
}

func TestCollaborationProviderRecoveryStopsAfterSecondFailure(t *testing.T) {
	for _, route := range []string{"forward", "passthrough", "bridge"} {
		for _, failure := range collaborationProviderFailureCases() {
			t.Run(route+"/"+failure.name, func(t *testing.T) {
				body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, fmt.Sprintf(`"model":"gpt-5","stream":%v`, failure.stream || route == "bridge"), 1))
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					collaborationTestResponse(failure.status, failure.body, failure.stream),
					collaborationTestResponse(failure.status, failure.body, failure.stream),
				}}
				svc := configureCollaborationTestService(t, upstream, true)
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = route == "passthrough"
				c := newCollaborationTestContext(body, 11)
				_, _, err := forwardCollaborationProviderTest(t, svc, c, account, body, route, 1)
				require.Error(t, err)
				require.Len(t, upstream.bodies, 2, "recovery must use exactly one modified attempt")
				require.NotContains(t, string(upstream.bodies[1]), "old-opaque")
				a := collaborationAttempt(c)
				require.NotNil(t, a)
				require.True(t, a.stopFailover)
				require.Empty(t, svc.collaborationCache.read(a.scope+":"+a.branch).omissions)
			})
		}
	}
}
