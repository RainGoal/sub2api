package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Send the observed placeholder immediately, then keep the upstream connection
// open before its terminal error. This reproduces provider timing, not only JSON.
func collaborationDelayedProviderResponse(t *testing.T, delay time.Duration, gate <-chan struct{}) (*http.Response, <-chan struct{}) {
	t.Helper()
	raw := collaborationProviderWhitespaceFailure()
	split := strings.Index(raw, `data: {"type":"response.output_text.done"`)
	require.Positive(t, split)
	reader, writer := io.Pipe()
	ready, stop, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	resp := collaborationTestResponse(http.StatusOK, "", true)
	resp.Body = reader
	go func() {
		defer close(done)
		defer func() { _ = writer.Close() }()
		if _, err := io.WriteString(writer, raw[:split]); err != nil {
			return
		}
		close(ready)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-stop:
			return
		}
		if gate != nil {
			select {
			case <-gate:
			case <-stop:
				return
			}
		}
		_, _ = io.WriteString(writer, raw[split:])
	}()
	t.Cleanup(func() {
		close(stop)
		_ = resp.Body.Close()
		_ = reader.Close()
		_ = writer.Close()
		<-done
	})
	return resp, ready
}

func TestCollaborationDelayedProviderRecoversAfterSixAndHalfSeconds(t *testing.T) {
	for _, route := range []string{"forward", "passthrough", "bridge"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			failure, _ := collaborationDelayedProviderResponse(t, 6500*time.Millisecond, nil)
			body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, `"model":"gpt-5","stream":true`, 1))
			success := "data: {\"type\":\"response.completed\",\"response\":" + collaborationCompletedJSON + "}\n\n"
			upstream := &httpUpstreamRecorder{responses: []*http.Response{failure,
				collaborationTestResponse(200, success, true), collaborationTestResponse(200, success, true)}}
			svc := configureCollaborationTestService(t, upstream, true)
			settings := enabledCollaborationTestSettings()
			settings.BlankPreludeWaitSeconds = 10
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_passthrough"] = route == "passthrough"
			c := newCollaborationTestContext(body, 11)
			started := time.Now()
			result, output, err := forwardCollaborationProviderTest(t, svc, c, account, body, route, 1)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.GreaterOrEqual(t, time.Since(started), 6500*time.Millisecond)
			require.Len(t, upstream.bodies, 2)
			require.Contains(t, string(upstream.bodies[0]), "old-opaque")
			require.NotContains(t, string(upstream.bodies[1]), "old-opaque")
			require.Contains(t, string(upstream.bodies[1]), "NEW_TASK_OK")
			require.Contains(t, output, "NEW_TASK_OK")
			require.NotContains(t, output, "resp_placeholder")
			require.NotContains(t, output, "msg_placeholder")
			require.NotContains(t, output, "invalid_encrypted_content")
			a := collaborationAttempt(c)
			require.Equal(t, map[string]bool{collaborationOmissionKey("amsg_old", collaborationHash("old-opaque")): true}, svc.collaborationCache.read(a.scope+":"+a.branch).omissions)
			if route != "bridge" {
				c = newCollaborationTestContext(body, 11)
			}
			_, _, err = forwardCollaborationProviderTest(t, svc, c, account, body, route, 2)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 3)
			require.NotContains(t, string(upstream.bodies[2]), "old-opaque")
		})
	}
}

func TestCollaborationDelayedProviderDefaultDoesNotRetryLateFailure(t *testing.T) {
	t.Parallel()
	failure, _ := collaborationDelayedProviderResponse(t, 2300*time.Millisecond, nil)
	body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, `"model":"gpt-5","stream":true`, 1))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{failure}}
	svc := configureCollaborationTestService(t, upstream, true)
	c := newCollaborationTestContext(body, 11)
	_, output, err := forwardCollaborationProviderTest(t, svc, c, newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 1)
	require.Contains(t, output, "resp_placeholder")
	require.Contains(t, output, "invalid_encrypted_content")
	a := collaborationAttempt(c)
	require.Empty(t, svc.collaborationCache.read(a.scope+":"+a.branch).omissions)
}

func TestCollaborationDelayedProviderShutdownDuringWaitPreventsRetry(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	failure, ready := collaborationDelayedProviderResponse(t, 0, gate)
	body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, `"model":"gpt-5","stream":true`, 1))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{failure}}
	svc := configureCollaborationTestService(t, upstream, true)
	settings := enabledCollaborationTestSettings()
	settings.BlankPreludeWaitSeconds = 10
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	updated := make(chan error, 1)
	go func() {
		<-ready
		settings.Enabled = false
		updated <- svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings)
		close(gate)
	}()
	c := newCollaborationTestContext(body, 11)
	_, _, err := forwardCollaborationProviderTest(t, svc, c, newOpenAIRejectedFieldTestAccount(), body, "forward", 1)
	require.NoError(t, <-updated)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 1, "a shutdown during the prelude must prevent a new paid attempt")
	a := collaborationAttempt(c)
	require.Empty(t, svc.collaborationCache.read(a.scope+":"+a.branch).omissions)
}

func TestCollaborationDelayedProviderDisabledAndPlaintextBypassProbe(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		enabled    bool
	}{
		{"master off", collaborationMixedRequest, false},
		{"new plaintext", `{"model":"gpt-5","input":[{"type":"agent_message","id":"amsg_new","content":[{"type":"input_text","text":"Complete NEW_TASK_OK"}]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(tc.body)
			svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, tc.enabled)
			settings := enabledCollaborationTestSettings()
			settings.Enabled, settings.BlankPreludeWaitSeconds = tc.enabled, 10
			require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
			c, account := newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount()
			svc.beginCollaborationAttempt(context.Background(), c, account, body, false)
			reader, writer := io.Pipe()
			resp := collaborationTestResponse(200, "", true)
			resp.Body = reader
			defer func() { _ = resp.Body.Close(); _ = writer.Close() }()
			started := time.Now()
			got, retry := svc.recoverCollaborationResponse(context.Background(), c, account, body, resp)
			require.False(t, retry)
			require.Equal(t, body, got)
			require.Same(t, reader, resp.Body, "bypassed requests must not wrap or start reading the response")
			require.Less(t, time.Since(started), time.Second)
		})
	}
}

func TestCollaborationDelayedProviderHostedToolDoesNotExtendWait(t *testing.T) {
	t.Parallel()
	body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, `"model":"gpt-5","tools":[{"type":"web_search"}]`, 1))
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	settings := enabledCollaborationTestSettings()
	settings.BlankPreludeWaitSeconds = 10
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	c, account := newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount()
	svc.beginCollaborationAttempt(context.Background(), c, account, body, false)
	resp, _ := collaborationDelayedProviderResponse(t, 5*time.Second, nil)
	started := time.Now()
	_, retry := svc.recoverCollaborationResponse(context.Background(), c, account, body, resp)
	require.False(t, retry)
	require.Less(t, time.Since(started), 4*time.Second, "requests that cannot replay must not enter the 10s wait")
}

func TestCollaborationDelayedProviderCancellationPreventsRecovery(t *testing.T) {
	t.Parallel()
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	settings := enabledCollaborationTestSettings()
	settings.BlankPreludeWaitSeconds = 10
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	c, account := newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.beginCollaborationAttempt(ctx, c, account, body, false)
	resp, ready := collaborationDelayedProviderResponse(t, 6500*time.Millisecond, nil)
	go func() {
		<-ready
		cancel()
	}()
	started := time.Now()
	_, retry := svc.recoverCollaborationResponse(ctx, c, account, body, resp)
	require.False(t, retry)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Less(t, time.Since(started), time.Second)
	require.Empty(t, collaborationAttempt(c).omissions)
}
