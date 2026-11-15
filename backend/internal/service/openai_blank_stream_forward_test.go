package service

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const blankForwardRequest = `{"model":"gpt-5","stream":true,"input":"hello"}`
const blankForwardFailed = `data: {"type":"response.failed","response":{"id":"resp_before","status":"failed","error":{"message":"Upstream request failed"}}}` + "\n\n"

func TestBlankStreamForwardReplayBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, terminal, tools string
		disabled, otherKey, retry     bool
	}{
		{name: "single space", prefix: collaborationProbeWhitespacePrelude, retry: true},
		{name: "empty text", prefix: strings.ReplaceAll(collaborationProbeWhitespacePrelude, `" "`, `""`), retry: true},
		{name: "ASCII whitespace", prefix: strings.ReplaceAll(collaborationProbeWhitespacePrelude, `" "`, strconv.Quote(" \t\r\n")), retry: true},
		{name: "disabled", prefix: collaborationProbeWhitespacePrelude, disabled: true},
		{name: "outside key scope", prefix: collaborationProbeWhitespacePrelude, otherKey: true},
		{name: "visible text", prefix: strings.ReplaceAll(collaborationProbeWhitespacePrelude, `" "`, `"hello"`)},
		{name: "tool start with empty arguments", prefix: collaborationOpenBlankPrelude() + "data: " + `{"type":"response.output_item.added","item":{"type":"function_call","arguments":""}}` + "\n\n"},
		{name: "empty reasoning start", prefix: collaborationOpenBlankPrelude() + "data: " + `{"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}` + "\n\n"},
		{name: "unknown event", prefix: collaborationOpenBlankPrelude() + "data: " + `{"type":"vendor.progress"}` + "\n\n"},
		{name: "unknown field", prefix: strings.Replace(collaborationProbeWhitespacePrelude, `"delta":" "`, `"delta":" ","audio":"opaque"`, 1)},
		{name: "hosted tool", prefix: collaborationProbeWhitespacePrelude, tools: `,"tools":[{"type":"web_search"}]`},
		{name: "terminal contains tool", prefix: collaborationProbeWhitespacePrelude, terminal: strings.Replace(blankForwardFailed, `"status":"failed"`, `"status":"failed","output":[{"type":"function_call","arguments":"{}"}]`, 1)},
		{name: "terminal contains text", prefix: collaborationProbeWhitespacePrelude, terminal: strings.Replace(blankForwardFailed, `"status":"failed"`, `"status":"failed","output":[{"id":"msg_placeholder","type":"message","role":"assistant","content":[{"type":"output_text","text":"result"}]}]`, 1)},
		{name: "policy failure", prefix: collaborationProbeWhitespacePrelude, terminal: strings.Replace(blankForwardFailed, "Upstream request failed", "content_policy violation", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terminal := tc.terminal
			if terminal == "" {
				terminal = blankForwardFailed
			}
			body := []byte(strings.TrimSuffix(blankForwardRequest, "}") + tc.tools + "}")
			upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, tc.prefix+terminal, true)}}
			svc := configureCollaborationTestService(t, upstream, true)
			settings := enabledCollaborationTestSettings()
			settings.PlaintextEnabled, settings.LegacyCipherRecoveryEnabled = false, false
			settings.BlankStreamFailoverEnabled = !tc.disabled
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
			keyID := int64(11)
			if tc.otherKey {
				keyID = 12
			}
			c := newCollaborationTestContext(body, keyID)
			_, output, err := forwardCollaborationProviderTest(t, svc, c, newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.Equal(t, tc.retry, errors.As(err, &failover), err.Error())
			if tc.retry {
				require.Empty(t, output, "failed attempt frames must never reach the client")
			} else {
				require.Contains(t, output, "response.failed")
			}
			require.Len(t, upstream.bodies, 1)
		})
	}
}

func TestBlankStreamForwardPreservesWhitespaceAndCompletion(t *testing.T) {
	for _, text := range []string{" ", " \t\n    hello"} {
		t.Run(strconv.Quote(text), func(t *testing.T) {
			prefix := strings.ReplaceAll(collaborationProbeWhitespacePrelude, `" "`, strconv.Quote(text))
			terminal := `data: {"type":"response.completed","response":{"id":"resp_before","status":"completed","output":[{"id":"msg_placeholder","type":"message","role":"assistant","content":[{"type":"output_text","text":` + strconv.Quote(text) + `,"annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":1}}}` + "\n\n"
			upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, prefix+terminal, true)}}
			svc := configureCollaborationTestService(t, upstream, true)
			settings := enabledCollaborationTestSettings()
			settings.BlankStreamFailoverEnabled = true
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
			body := []byte(blankForwardRequest)
			result, output, err := forwardCollaborationProviderTest(t, svc, newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
			require.NoError(t, err)
			require.Contains(t, output, `"delta":`+strconv.Quote(text))
			require.Contains(t, output, "response.completed")
			require.Equal(t, 3, result.Usage.InputTokens)
			require.Equal(t, 1, result.Usage.OutputTokens)
		})
	}
}

func TestBlankStreamForwardNextAccountHasNoFailedAttemptFrames(t *testing.T) {
	body := []byte(blankForwardRequest)
	success := "data: " + `{"type":"response.completed","response":` + collaborationCompletedJSON + "}\n\n"
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		collaborationTestResponse(200, collaborationProbeWhitespacePrelude+blankForwardFailed, true),
		collaborationTestResponse(200, success, true),
	}}
	svc := configureCollaborationTestService(t, upstream, true)
	settings := enabledCollaborationTestSettings()
	settings.BlankStreamFailoverEnabled = true
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	c := newCollaborationTestContext(body, 11)
	_, output, err := forwardCollaborationProviderTest(t, svc, c, newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Empty(t, output)
	secondAccount := newOpenAIRejectedFieldTestAccount()
	secondAccount.ID++
	result, output, err := forwardCollaborationProviderTest(t, svc, c, secondAccount, body, "forward", 1)
	require.NoError(t, err)
	require.Contains(t, output, "NEW_TASK_OK")
	require.NotContains(t, output, "resp_before")
	require.NotContains(t, output, "msg_placeholder")
	require.NotContains(t, output, "response.failed")
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Len(t, upstream.bodies, 2)
}

func TestBlankStreamForwardPolicyFailureNearStagingLimitDoesNotRetry(t *testing.T) {
	prefix := collaborationOpenBlankPrelude()
	prefix += ":" + strings.Repeat(" ", openAIBlankStreamMaxBytes-len(prefix)-64) + "\n\n"
	failure := strings.Replace(blankForwardFailed, "Upstream request failed", "content_policy violation", 1)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, prefix+failure, true)}}
	svc := configureCollaborationTestService(t, upstream, true)
	settings := enabledCollaborationTestSettings()
	settings.BlankStreamFailoverEnabled = true
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	body := []byte(blankForwardRequest)
	_, output, err := forwardCollaborationProviderTest(t, svc, newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.NotErrorAs(t, err, &failover)
	require.Contains(t, output, "response.failed")
}

func TestBlankStreamForwardKeepsEmptyCompletionPolicy(t *testing.T) {
	for _, actualWhitespace := range []bool{false, true} {
		t.Run(strconv.FormatBool(actualWhitespace), func(t *testing.T) {
			prefix := collaborationProbeWhitespacePrelude
			if !actualWhitespace {
				prefix = prefix[:strings.Index(prefix, `data: {"type":"response.content_part.added"`)]
			}
			terminal := `data: {"type":"response.completed","response":{"id":"resp_before","status":"completed","output":[]}}` + "\n\n"
			upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, prefix+terminal, true)}}
			svc := configureCollaborationTestService(t, upstream, true)
			settings := enabledCollaborationTestSettings()
			settings.BlankStreamFailoverEnabled = true
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
			body := []byte(blankForwardRequest)
			_, output, err := forwardCollaborationProviderTest(t, svc, newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
			if actualWhitespace {
				require.NoError(t, err)
				require.Contains(t, output, `"delta":" "`)
			} else {
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover, "an empty message shell must not bypass the existing silent-refusal policy")
				require.Empty(t, output)
			}
		})
	}
}
