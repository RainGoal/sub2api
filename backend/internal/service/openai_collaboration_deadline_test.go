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

type collaborationDelayedHeadersUpstream struct {
	*httpUpstreamRecorder
	delay time.Duration
}

func (u *collaborationDelayedHeadersUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	timer := time.NewTimer(u.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return u.httpUpstreamRecorder.Do(req, proxyURL, accountID, concurrency)
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func TestCollaborationBlankWaitHonorsFirstOutputDeadline(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	failure := collaborationTestResponse(http.StatusOK, "", true)
	failure.Body = reader
	// Include the provider's actual space delta. Expired first-output budget
	// must win even if the ordinary stream handler would count it as progress.
	raw := collaborationProviderWhitespaceFailure()
	split := strings.Index(raw, `data: {"type":"response.output_text.done"`)
	require.Positive(t, split)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.WriteString(writer, raw[:split])
	}()
	t.Cleanup(func() {
		_ = failure.Body.Close()
		_ = reader.Close()
		_ = writer.Close()
		<-done
	})
	body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, `"model":"gpt-5","stream":true`, 1))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{failure}}
	svc := configureCollaborationTestService(t, upstream, true)
	// Compress the production 30s minimum to 1s in memory. Headers consume
	// most of it; the configured blank prelude must not create a fresh 10s budget.
	svc.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 1
	svc.httpUpstream = &collaborationDelayedHeadersUpstream{httpUpstreamRecorder: upstream, delay: 700 * time.Millisecond}
	settings := enabledCollaborationTestSettings()
	settings.BlankPreludeWaitSeconds = 10
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
	c := newCollaborationTestContext(body, 11)
	started := time.Now()
	_, err := svc.Forward(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body)
	elapsed := time.Since(started)
	var timeout *UpstreamFailoverError
	require.ErrorAs(t, err, &timeout)
	require.Contains(t, string(timeout.ResponseBody), "first_output_timeout")
	require.GreaterOrEqual(t, elapsed, time.Second)
	require.Less(t, elapsed, 2*time.Second)
	require.Len(t, upstream.bodies, 1)
	require.False(t, collaborationAttempt(c).recoveryUsed)
}
