package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const blankStreamRouteRequest = `{"model":"gpt-5","stream":true,"input":[{"role":"user","content":"hello"}]}`
const blankStreamRouteFailure = `data: {"type":"response.failed","response":{"id":"resp_before","status":"failed","error":{"message":"Upstream request failed"},"output":[{"id":"msg_placeholder","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":" ","annotations":[]}]}]}}` + "\n\n"

func TestBlankStreamRoutesFailureSafety(t *testing.T) {
	for _, route := range []string{"passthrough", "bridge"} {
		for _, tc := range []struct {
			name, prelude, failure, request string
			disabled, failover              bool
		}{
			{name: "single space", failover: true},
			{name: "tabs and newlines", prelude: strings.ReplaceAll(collaborationProbeWhitespacePrelude, `" "`, `" \t\r\n "`), failover: true},
			{name: "disabled", disabled: true},
			{name: "text before failure", prelude: strings.Replace(collaborationProbeWhitespacePrelude, `"delta":" "`, `"delta":"hello"`, 1)},
			{name: "empty function call", prelude: `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"call_1","name":"run","arguments":""}}` + "\n\n"},
			{name: "empty reasoning item", prelude: `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}` + "\n\n"},
			{name: "unknown semantic field", prelude: strings.Replace(collaborationProbeWhitespacePrelude, `"delta":" "`, `"delta":" ","audio":"payload"`, 1)},
			{name: "terminal contains tool", failure: `data: {"type":"response.failed","response":{"id":"resp_before","status":"failed","error":{"message":"Upstream request failed"},"output":[{"type":"function_call","id":"call_1","name":"run","arguments":"{}"}]}}` + "\n\n"},
			{name: "terminal contains text", failure: strings.Replace(blankStreamRouteFailure, `"text":" "`, `"text":"result"`, 1)},
			{name: "hosted tool request", request: strings.Replace(blankStreamRouteRequest, `"stream":true`, `"stream":true,"tools":[{"type":"web_search"}]`, 1)},
			{name: "policy failure", failure: strings.Replace(blankStreamRouteFailure, `"message":"Upstream request failed"`, `"message":"Content policy violation","code":"content_policy_violation"`, 1)},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				body := []byte(firstNonEmpty(tc.request, blankStreamRouteRequest))
				prelude, failure := tc.prelude, tc.failure
				if prelude == "" {
					prelude = collaborationProbeWhitespacePrelude
				}
				if failure == "" {
					failure = blankStreamRouteFailure
				}
				raw := prelude + failure
				for _, line := range strings.Split(raw, "\n") {
					_, rewriteErr := rewriteOpenAIClientSSELine(line, "gpt-5")
					require.NoError(t, rewriteErr, line)
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, raw, true)}}
				svc := configureCollaborationTestService(t, upstream, true)
				settings := enabledCollaborationTestSettings()
				settings.PlaintextEnabled, settings.LegacyCipherRecoveryEnabled = false, false
				settings.BlankStreamFailoverEnabled = !tc.disabled
				require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = route == "passthrough"
				c := newCollaborationTestContext(body, 11)
				_, output, err := forwardCollaborationProviderTest(t, svc, c, account, body, route, 1)
				require.Error(t, err)
				var failover *UpstreamFailoverError
				if tc.failover {
					require.ErrorAs(t, err, &failover)
					require.Empty(t, output, "failed attempt must not leak placeholder events")
				} else {
					require.NotErrorAs(t, err, &failover)
					require.Contains(t, output, "response.failed")
				}
				require.Len(t, upstream.bodies, 1, "the route delegates retries to the existing failover budget")
			})
		}
	}
}

func TestBlankStreamRoutesPreserveSuccessfulWhitespace(t *testing.T) {
	for _, route := range []string{"passthrough", "bridge"} {
		for _, tc := range []struct {
			name, prelude, terminal string
		}{
			{"blank completion", collaborationProbeWhitespacePrelude, `data: {"type":"response.completed","response":{"id":"resp_before","status":"completed","output":[]}}` + "\n\n"},
			{"formatted text", collaborationProbeWhitespacePrelude[:strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.done"`)], `data: {"type":"response.output_text.delta","item_id":"msg_placeholder","content_index":0,"output_index":0,"delta":"  hello\n"}` + "\n\n" + `data: {"type":"response.completed","response":{"id":"resp_before","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n\n"},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				body := []byte(blankStreamRouteRequest)
				raw := tc.prelude + tc.terminal
				upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, raw, true)}}
				svc := configureCollaborationTestService(t, upstream, true)
				settings := enabledCollaborationTestSettings()
				settings.PlaintextEnabled, settings.LegacyCipherRecoveryEnabled = false, false
				settings.BlankStreamFailoverEnabled = true
				require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = route == "passthrough"
				c := newCollaborationTestContext(body, 11)
				_, output, err := forwardCollaborationProviderTest(t, svc, c, account, body, route, 1)
				require.NoError(t, err)
				require.Contains(t, output, `"delta":" "`)
				require.Contains(t, output, "response.completed")
				if tc.name == "formatted text" {
					require.Contains(t, output, `"delta":"  hello\n"`)
				}
				require.Len(t, upstream.bodies, 1)
			})
		}
	}
}

func TestBlankStreamRoutesBoundStaging(t *testing.T) {
	boundary := strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.done"`)
	const delta = `data: {"type":"response.output_text.delta","item_id":"msg_placeholder","content_index":0,"output_index":0,"delta":" "}` + "\n\n"
	raw := collaborationProbeWhitespacePrelude[:boundary] + strings.Repeat(delta, 2*openAIBlankStreamMaxBytes/len(delta)+1)
	for _, route := range []string{"forward", "passthrough", "bridge"} {
		t.Run(route, func(t *testing.T) {
			body := []byte(blankStreamRouteRequest)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, raw, true)}}
			svc := configureCollaborationTestService(t, upstream, true)
			settings := enabledCollaborationTestSettings()
			settings.BlankStreamFailoverEnabled = true
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_passthrough"] = route == "passthrough"
			_, output, err := forwardCollaborationProviderTest(t, svc, newCollaborationTestContext(body, 11), account, body, route, 1)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Contains(t, string(failover.ResponseBody), "staging limit")
			require.Empty(t, output)
		})
	}
}

func TestBlankStreamRoutesStopStalledPrelude(t *testing.T) {
	for _, route := range []string{"forward", "passthrough", "bridge"} {
		t.Run(route, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer func() { _ = reader.Close(); _ = writer.Close() }()
			resp := collaborationTestResponse(200, "", true)
			resp.Body = reader
			upstream := &httpUpstreamRecorder{responses: []*http.Response{resp}}
			svc := configureCollaborationTestService(t, upstream, true)
			svc.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 1
			settings := enabledCollaborationTestSettings()
			settings.BlankStreamFailoverEnabled = true
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
			body := []byte(blankStreamRouteRequest)
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_passthrough"] = route == "passthrough"
			go func() { _, _ = io.WriteString(writer, collaborationProbeWhitespacePrelude) }()
			started := time.Now()
			_, output, err := forwardCollaborationProviderTest(t, svc, newCollaborationTestContext(body, 11), account, body, route, 1)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Equal(t, http.StatusGatewayTimeout, failover.StatusCode)
			require.True(t, failover.SafeToFailoverAfterWrite)
			require.Empty(t, output)
			require.Less(t, time.Since(started), 3*time.Second)
		})
	}
}

func TestBlankStreamRoutesCancellationDoesNotReplay(t *testing.T) {
	for _, route := range []string{"forward", "passthrough", "bridge"} {
		t.Run(route, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			defer func() { _ = reader.Close(); _ = writer.Close() }()
			resp := collaborationTestResponse(200, "", true)
			resp.Body = reader
			upstream := &httpUpstreamRecorder{responses: []*http.Response{resp}}
			svc := configureCollaborationTestService(t, upstream, true)
			settings := enabledCollaborationTestSettings()
			settings.BlankStreamFailoverEnabled = true
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(ctx, settings))
			body := []byte(blankStreamRouteRequest)
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_passthrough"] = route == "passthrough"
			c := newCollaborationTestContext(body, 11)
			c.Request = c.Request.WithContext(ctx)
			go func() {
				_, _ = io.WriteString(writer, collaborationProbeWhitespacePrelude)
				cancel()
			}()
			var err error
			var output strings.Builder
			if route == "bridge" {
				_, err = svc.proxyOpenAIWSHTTPBridgeTurn(ctx, c, account, "test-token", body, len(body), "gpt-5", "", "", "", "", 1,
					func(event []byte) error { _, _ = output.Write(event); return nil })
			} else {
				_, err = svc.Forward(ctx, c, account, body)
				recorder, _ := c.Get("collaboration_test_recorder")
				recorded, ok := recorder.(*httptest.ResponseRecorder)
				require.True(t, ok)
				_, _ = output.WriteString(recorded.Body.String())
			}
			require.ErrorIs(t, err, context.Canceled)
			var failover *UpstreamFailoverError
			require.NotErrorAs(t, err, &failover)
			require.Empty(t, output.String())
			require.Len(t, upstream.bodies, 1)
		})
	}
}
