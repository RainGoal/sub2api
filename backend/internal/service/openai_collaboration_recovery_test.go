package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const collaborationRecoveryMixedBody = `{"model":"gpt-5","previous_response_id":"resp_keep","input":[
{"type":"reasoning","id":"rs_keep","encrypted_content":"reasoning-cipher"},
{"type":"agent_message","id":"amsg_old","author":"/root","recipient":"/root/child","role":"developer","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nPayload:\n"},{"type":"encrypted_content","encrypted_content":"cipher-one"},{"type":"encrypted_content","encrypted_content":"cipher-two"},{"type":"future_attachment","value":{"encrypted_content":"do-not-touch"}}]},
{"type":"function_call_output","call_id":"call_keep","output":{"encrypted_content":"function-output-keep"}},
{"type":"agent_message","id":"amsg_other","content":[{"type":"encrypted_content","encrypted_content":"cipher-three"}]},
{"type":"message","role":"user","content":"Complete independent NEW TASK"},
{"type":"compaction","encrypted_content":"compaction-cipher"}]}`

func collaborationRecoveryError(t *testing.T, message, parameter string) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"error": map[string]any{
		"code": "invalid_encrypted_content", "message": message, "param": parameter,
	}})
	require.NoError(t, err)
	return payload
}

func TestOpenAICollaborationRecoveryExactPathPreservesOpaqueRemainder(t *testing.T) {
	t.Parallel()
	body := []byte(collaborationRecoveryMixedBody)
	before := string(body)
	result, err := recoverOpenAICollaborationEncryptedContent(body, http.StatusBadRequest,
		collaborationRecoveryError(t, "Failed to decrypt amsg_old at input[1].content[1].", "input[1].content[1]"), true)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "exact_path", result.Selection)
	require.Equal(t, []openAICollaborationOmission{{MessageID: "amsg_old", InputIndex: 1, ContentIndex: 1, Digest: openAIEncryptedContentDigest("cipher-one")}}, result.Omissions)
	require.Equal(t, before, string(body), "the caller's canonical request is immutable")
	require.Equal(t, "agent_message", gjson.GetBytes(result.Body, "input.1.type").String())
	require.Equal(t, "cipher-two", gjson.GetBytes(result.Body, "input.1.content.1.encrypted_content").String())
	require.Equal(t, "future_attachment", gjson.GetBytes(result.Body, "input.1.content.2.type").String())
	require.Contains(t, gjson.GetBytes(result.Body, "input.1.content.3.text").String(), openAICollaborationMissingContentNotice)
	require.NotContains(t, string(result.Body), "cipher-one")
	require.Contains(t, string(result.Body), "cipher-three")
}

func TestOpenAICollaborationRecoveryMessageIDKeepsEnvelopeAndNewTask(t *testing.T) {
	t.Parallel()
	result, err := recoverOpenAICollaborationEncryptedContent([]byte(collaborationRecoveryMixedBody), http.StatusOK,
		[]byte(`{"type":"response.failed","response":{"error":{"message":"Cannot decrypt agent message amsg_old"}}}`), false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "message_id", result.Selection)
	require.Len(t, result.Omissions, 2)
	require.Equal(t, "message", gjson.GetBytes(result.Body, "input.1.type").String())
	require.Equal(t, "user", gjson.GetBytes(result.Body, "input.1.role").String())
	require.Equal(t, "Message Type: NEW_TASK\nPayload:\n", gjson.GetBytes(result.Body, "input.1.content.0.text").String())
	require.Equal(t, "future_attachment", gjson.GetBytes(result.Body, "input.1.content.1.type").String())
	notice := gjson.GetBytes(result.Body, "input.1.content.2.text").String()
	require.Contains(t, notice, `id="amsg_old"; author="/root"; recipient="/root/child"`)
	require.Contains(t, notice, "does not mean the task was completed")
	for _, path := range []string{"model", "previous_response_id", "input.0", "input.2", "input.3", "input.4", "input.5"} {
		require.JSONEq(t, gjson.Get(collaborationRecoveryMixedBody, path).Raw, gjson.GetBytes(result.Body, path).Raw)
	}
}

func TestOpenAICollaborationRecoveryUnlocatedSelectionIsExplicit(t *testing.T) {
	t.Parallel()
	errorBody := collaborationRecoveryError(t, "Encrypted content is invalid", "")
	disabled, err := recoverOpenAICollaborationEncryptedContent([]byte(collaborationRecoveryMixedBody), 400, errorBody, false)
	require.NoError(t, err)
	require.Nil(t, disabled)
	result, err := recoverOpenAICollaborationEncryptedContent([]byte(collaborationRecoveryMixedBody), 400, errorBody, true)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "unlocated_agent_set", result.Selection)
	require.Len(t, result.Omissions, 3)
	require.Equal(t, "message", gjson.GetBytes(result.Body, "input.3.type").String())
	require.Equal(t, "user", gjson.GetBytes(result.Body, "input.3.role").String())
	require.Contains(t, gjson.GetBytes(result.Body, "input.3.content.0.text").String(), "ask the parent agent")
	for _, path := range []string{"previous_response_id", "input.0", "input.2", "input.4", "input.5"} {
		require.JSONEq(t, gjson.Get(collaborationRecoveryMixedBody, path).Raw, gjson.GetBytes(result.Body, path).Raw)
	}
	for _, omission := range result.Omissions {
		require.Len(t, omission.Digest, 64)
	}
}

func TestOpenAICollaborationRecoveryRejectsUnsafeLocationWithoutBulkFallback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, message, param string }{
		{"out of range", "invalid", "input[99].content[0]"},
		{"wrong content type", "invalid", "input[1].content[0]"},
		{"negative index", "invalid input[-1].content[1]", ""},
		{"non numeric index", "invalid input[x].content[1]", ""},
		{"unsupported pointer", "invalid /input/1/content/1", ""},
		{"incomplete path", "invalid input[1]", ""},
		{"unknown suffix", "invalid input[1].content[1].text", ""},
		{"unrelated parameter", "invalid", "model"},
		{"unknown message", "Cannot decrypt amsg_unknown", ""},
		{"conflicting ID", "Cannot decrypt amsg_other", "input[1].content[1]"},
		{"ambiguous IDs", "Cannot decrypt amsg_old or amsg_other", ""},
		{"multiple paths one invalid", "invalid input[1].content[1] and input[99].content[0]", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := recoverOpenAICollaborationEncryptedContent([]byte(collaborationRecoveryMixedBody), 400,
				collaborationRecoveryError(t, tc.message, tc.param), true)
			require.ErrorIs(t, err, errOpenAICollaborationLocation)
			require.Nil(t, result)
		})
	}
	duplicate := strings.Replace(collaborationRecoveryMixedBody, "amsg_other", "amsg_old", 1)
	result, err := recoverOpenAICollaborationEncryptedContent([]byte(duplicate), 400,
		collaborationRecoveryError(t, "Cannot decrypt amsg_old", ""), true)
	require.ErrorIs(t, err, errOpenAICollaborationLocation)
	require.Nil(t, result)
}

func TestOpenAICollaborationRecoveryLeavesReasoningAndMissingInputToExistingPolicy(t *testing.T) {
	t.Parallel()
	for _, param := range []string{"input[0].encrypted_content", "input[5].encrypted_content"} {
		result, err := recoverOpenAICollaborationEncryptedContent([]byte(collaborationRecoveryMixedBody), 400,
			collaborationRecoveryError(t, "invalid encrypted content", param), true)
		require.NoError(t, err)
		require.Nil(t, result)
	}
	for _, body := range []string{`{"previous_response_id":"resp_server_only"}`, `{"input":"hello"}`, `{"input":[{"type":"message","role":"user","encrypted_content":"not-agent"}]}`} {
		result, err := recoverOpenAICollaborationEncryptedContent([]byte(body), 400,
			collaborationRecoveryError(t, "invalid encrypted content", ""), true)
		require.NoError(t, err)
		require.Nil(t, result)
	}
}

func TestOpenAICollaborationEncryptionFailureClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		status  int
		payload string
		want    bool
	}{
		{"typed HTTP", 400, `{"error":{"code":"invalid_encrypted_content"}}`, true},
		{"SSE failed", 200, `{"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_encrypted_content"}}}`, true},
		{"bare error", 200, `{"type":"error","error":{"message":"Could not decrypt amsg_abc"}}`, true},
		{"assignment", 400, `{"error":{"code":"basispoints_request_invalid","message":"basispoints cannot read a new encrypted agent assignment"}}`, true},
		{"output decode", 422, `{"error":"Encrypted function output could not be decoded"}`, true},
		{"ordinary invalid", 400, `{"error":{"code":"basispoints_request_invalid","message":"bad request"}}`, false},
		{"ordinary decrypt", 400, `{"error":{"message":"Could not decrypt unknown content"}}`, false},
		{"HTTP rate limit", 429, `{"error":{"code":"invalid_encrypted_content"}}`, false},
		{"HTTP auth", 401, `{"error":{"code":"invalid_encrypted_content"}}`, false},
		{"HTTP forbidden", 403, `{"error":{"code":"invalid_encrypted_content"}}`, false},
		{"transport error", 502, `{"error":{"code":"invalid_encrypted_content"}}`, false},
		{"SSE rate limit", 200, `{"type":"error","error":{"type":"rate_limit_error","code":"invalid_encrypted_content"}}`, false},
		{"SSE policy", 200, `{"type":"error","error":{"type":"content_policy_violation","message":"Cannot decode encrypted function output"}}`, false},
		{"policy message", 400, `{"error":{"code":"invalid_encrypted_content","message":"Content policy violation"}}`, false},
		{"completed is not failed", 200, `{"type":"response.completed","response":{"error":{"code":"invalid_encrypted_content"}}}`, false},
		{"malformed", 400, `{"error":`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isOpenAICollaborationEncryptedContentError(tc.status, []byte(tc.payload)))
		})
	}
}

func TestOpenAICollaborationRecoveryWrappedErrorAndMultipleExactPaths(t *testing.T) {
	t.Parallel()
	inner := `{"error":{"code":"invalid_encrypted_content","message":"Failed at input[1].content[1] and input.3.content.0"}}`
	wrapped, err := json.Marshal(map[string]any{"error": map[string]any{"message": inner + " (trace: omitted)"}})
	require.NoError(t, err)
	result, err := recoverOpenAICollaborationEncryptedContent([]byte(collaborationRecoveryMixedBody), 400, wrapped, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Omissions, 2)
	require.Equal(t, "agent_message", gjson.GetBytes(result.Body, "input.1.type").String())
	require.Equal(t, "message", gjson.GetBytes(result.Body, "input.3.type").String())
	require.False(t, isOpenAICollaborationEncryptedContentError(400, []byte(fmt.Sprintf(`{"error":{"code":"invalid_encrypted_content","message":%q}}`, strings.Repeat("x", 64*1024)))))
}

func TestOpenAICollaborationRecoveryProtectsProvenPlaintextInMixedMessage(t *testing.T) {
	t.Parallel()
	const completeNewTask = "Current full task: independently inspect the interface"
	body := []byte(fmt.Sprintf(`{"input":[{"type":"agent_message","id":"amsg_mixed","content":[{"type":"input_text","text":"Payload:\n"},{"type":"encrypted_content","encrypted_content":"opaque-old"},{"type":"encrypted_content","encrypted_content":%q}]}]}`, completeNewTask))
	proofs := map[string]bool{openAIEncryptedContentDigest(completeNewTask): true}
	for _, parameter := range []string{"", "input[0].content[1]"} {
		result, err := recoverOpenAICollaborationEncryptedContentWithPlaintext(body, 400,
			collaborationRecoveryError(t, "invalid encrypted content", parameter), true, proofs)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Omissions, 1)
		require.Equal(t, 1, result.Omissions[0].ContentIndex)
		require.Equal(t, openAIEncryptedContentDigest("opaque-old"), result.Omissions[0].Digest)
		require.Equal(t, "message", gjson.GetBytes(result.Body, "input.0.type").String())
		require.Equal(t, completeNewTask, gjson.GetBytes(result.Body, "input.0.content.1.text").String())
	}
	result, err := recoverOpenAICollaborationEncryptedContentWithPlaintext(body, 400,
		collaborationRecoveryError(t, "invalid encrypted content", "input[0].content[2]"), true, proofs)
	require.ErrorIs(t, err, errOpenAICollaborationLocation)
	require.Nil(t, result)
	require.Equal(t, completeNewTask, gjson.GetBytes(body, "input.0.content.2.encrypted_content").String())
}
