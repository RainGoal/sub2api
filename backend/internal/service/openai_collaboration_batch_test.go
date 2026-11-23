package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const collaborationBatchBody = `{
  "model":"gpt-5",
  "input":[
    {"type":"agent_message","id":"amsg_old","author":"/root","recipient":"/root/child","content":[
      {"type":"input_text","text":"Keep this assignment"},
      {"type":"encrypted_content","encrypted_content":"old-opaque"}
    ]},
    {"type":"agent_message","id":"amsg_other","author":"/root/child","recipient":"/root","content":[
      {"type":"encrypted_content","encrypted_content":"other-opaque"},
      {"type":"encrypted_content","encrypted_content":"Previously verified plaintext"}
    ]},
    {"type":"reasoning","id":"rs_keep","encrypted_content":"reasoning-cipher"},
    {"type":"message","role":"user","content":"Complete the new independent task"}
  ]
}`

func configureCollaborationBatchTest(t *testing.T, upstream *httpUpstreamRecorder, enabled bool) *OpenAIGatewayService {
	t.Helper()
	svc := configureCollaborationTestService(t, upstream, true)
	settings := enabledCollaborationTestSettings()
	settings.BatchLegacyCipherRecoveryEnabled = enabled
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	return svc
}

func TestCollaborationBatchRecoverySendsOneRetryWithProtectedContent(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			enabled   bool
			wantBatch bool
			status    int
			failure   string
		}{
			{"default_off", false, false, http.StatusBadRequest, collaborationFailedJSON},
			{"message_id", true, true, http.StatusBadRequest, collaborationFailedJSON},
			{"exact_path", true, true, http.StatusUnprocessableEntity, `{"error":{"code":"invalid_encrypted_content","param":"input[0].content[1].encrypted_content"}}`},
			{"message_only", true, false, http.StatusBadRequest, `{"error":{"message":"Unable to decrypt amsg_old"}}`},
			{"tool_output_error", true, false, http.StatusBadRequest, `{"error":{"code":"invalid_encrypted_content","message":"Encrypted function output could not be decrypted for item amsg_old"}}`},
		} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", tc.name, passthrough), func(t *testing.T) {
				body := []byte(collaborationBatchBody)
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					collaborationTestResponse(tc.status, tc.failure, false),
					collaborationTestResponse(http.StatusOK, collaborationCompletedJSON, false),
				}}
				if tc.wantBatch {
					upstream.responses = append(upstream.responses, collaborationTestResponse(http.StatusOK, collaborationCompletedJSON, false))
				}
				svc := configureCollaborationBatchTest(t, upstream, tc.enabled)
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = passthrough
				ctx := context.Background()
				seed := svc.beginCollaborationAttempt(ctx, newCollaborationTestContext(body, 11), account, body, false)
				svc.collaborationCache.update(seed.scope, func(entry *openAICollaborationCacheEntry) {
					entry.plaintext[openAIEncryptedContentDigest("Previously verified plaintext")] = true
				})

				result, err := svc.Forward(ctx, newCollaborationTestContext(body, 11), account, body)
				require.NoError(t, err)
				require.Equal(t, 2, result.Usage.OutputTokens)
				require.Len(t, upstream.bodies, 2)
				original, retry := upstream.bodies[0], upstream.bodies[1]
				require.Equal(t, "old-opaque", gjson.GetBytes(original, "input.0.content.1.encrypted_content").String())
				require.Equal(t, "other-opaque", gjson.GetBytes(original, "input.1.content.0.encrypted_content").String())
				require.False(t, gjson.GetBytes(retry, "input.0.content.#(type==encrypted_content)").Exists())
				require.Equal(t, "Keep this assignment", gjson.GetBytes(retry, "input.0.content.0.text").String())
				require.Contains(t, gjson.GetBytes(retry, "input.0.content.1.text").String(), openAICollaborationMissingContentNotice)
				require.Equal(t, "reasoning-cipher", gjson.GetBytes(retry, "input.2.encrypted_content").String())
				require.Equal(t, "Complete the new independent task", gjson.GetBytes(retry, "input.3.content").String())
				if tc.wantBatch {
					require.Equal(t, "Previously verified plaintext", gjson.GetBytes(retry, "input.1.content.0.text").String())
					require.Contains(t, gjson.GetBytes(retry, "input.1.content.1.text").String(), openAICollaborationMissingContentNotice)
					require.False(t, gjson.GetBytes(retry, "input.1.content.#(type==encrypted_content)").Exists())

					_, err = svc.Forward(ctx, newCollaborationTestContext(body, 11), account, body)
					require.NoError(t, err)
					require.Len(t, upstream.bodies, 3)
					require.JSONEq(t, string(original), string(upstream.bodies[2]))
				} else {
					require.Equal(t, "other-opaque", gjson.GetBytes(retry, "input.1.content.0.encrypted_content").String())
				}
			})
		}
	}
}

func TestCollaborationBatchRecoveryDoesNotExpandWithHostedTools(t *testing.T) {
	body := []byte(strings.Replace(collaborationBatchBody, `"model":"gpt-5"`, `"model":"gpt-5","tools":[{"type":"web_search"}]`, 1))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(http.StatusBadRequest, collaborationFailedJSON, false),
		collaborationTestResponse(http.StatusOK, collaborationCompletedJSON, false),
	}}
	svc := configureCollaborationBatchTest(t, upstream, true)
	_, err := svc.Forward(context.Background(), newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, string(upstream.bodies[1]), "other-opaque")
	require.NotContains(t, string(upstream.bodies[1]), "old-opaque")
}

func TestCollaborationBatchRecoveryRejectsAmbiguousInput(t *testing.T) {
	for _, body := range []string{
		`{"tools":[],"tools":[{"type":"web_search"}]}`,
		`{"tools":[{"type":"function","type":"web_search"}]}`,
		`{"input":[{"type":"message","type":"additional_tools","tools":[{"type":"web_search"}]}]}`,
		`{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"keep","type":"encrypted_content","encrypted_content":"opaque"}]}]}`,
	} {
		require.False(t, collaborationHasHostedTools([]byte(body)))
		require.True(t, collaborationHasUnsafeReplayInput([]byte(body), true))
	}
}

func TestCollaborationBatchRecoveryRespectsRetrySafety(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		failure string
	}{
		{"unmapped_id", http.StatusBadRequest, `{"error":{"code":"invalid_encrypted_content","message":"Unable to decrypt amsg_unknown"}}`},
		{"conflicting_ids", http.StatusBadRequest, `{"error":{"code":"invalid_encrypted_content","message":"Unable to decrypt amsg_old and amsg_other"}}`},
		{"unrelated_error", http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded","message":"Rate limit exceeded"}}`},
		{"after_valid_output", http.StatusOK,
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Started\"}\n\n" +
				"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"message\":\"Unable to decrypt amsg_old\"}}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := tc.status == http.StatusOK
			body := []byte(collaborationBatchBody)
			if stream {
				body = append([]byte(`{"stream":true,`), body[1:]...)
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				collaborationTestResponse(tc.status, tc.failure, stream),
				collaborationTestResponse(http.StatusOK, collaborationCompletedJSON, false),
			}}
			svc := configureCollaborationBatchTest(t, upstream, true)
			_, err := svc.Forward(context.Background(), newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body)
			require.Error(t, err)
			require.Len(t, upstream.bodies, 1)
			require.Contains(t, string(upstream.bodies[0]), "other-opaque")
		})
	}
}

func TestCollaborationBatchRecoveryStopsAfterFailedRetry(t *testing.T) {
	body := []byte(collaborationBatchBody)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(http.StatusBadRequest, collaborationFailedJSON, false),
		collaborationTestResponse(http.StatusBadRequest, `{"error":{"code":"invalid_encrypted_content","message":"Unable to decrypt amsg_other"}}`, false),
		collaborationTestResponse(http.StatusOK, collaborationCompletedJSON, false),
	}}
	svc := configureCollaborationBatchTest(t, upstream, true)
	c := newCollaborationTestContext(body, 11)
	_, err := svc.Forward(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 2)
	require.NotContains(t, string(upstream.bodies[1]), "other-opaque")
	attempt := collaborationAttempt(c)
	require.Empty(t, svc.collaborationCache.read(attempt.scope+":"+attempt.branch).omissions)
}

func TestCollaborationBatchRecoveryOnVerifiedBlankPrelude(t *testing.T) {
	body := []byte(strings.Replace(collaborationBatchBody, `"model":"gpt-5"`, `"model":"gpt-5","stream":true`, 1))
	failure := strings.Replace(collaborationProviderWhitespaceFailure(), "Encrypted content could not be verified.", "Unable to decrypt amsg_old", 1)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(http.StatusOK, failure, true),
		collaborationTestResponse(http.StatusOK, "data: {\"type\":\"response.completed\",\"response\":"+collaborationCompletedJSON+"}\n\n", true),
	}}
	svc := configureCollaborationBatchTest(t, upstream, true)
	c := newCollaborationTestContext(body, 11)
	_, output, err := forwardCollaborationProviderTest(t, svc, c, newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, string(upstream.bodies[0]), "other-opaque")
	require.NotContains(t, string(upstream.bodies[1]), "other-opaque")
	require.Contains(t, output, "NEW_TASK_OK")
	require.NotContains(t, output, "resp_placeholder")
	require.NotContains(t, output, "invalid_encrypted_content")
}
