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

func collaborationOpenBlankPrelude() string {
	return collaborationProbeWhitespacePrelude[:strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.done"`)]
}

func TestCollaborationProbeLongWaitRequiresBlankPart(t *testing.T) {
	for _, tc := range []struct{ name, contentType, prefix string }{
		{"silent", "text/event-stream", ""},
		{"lifecycle and heartbeat", "text/event-stream", collaborationProbeCreated + ": keepalive\n\n"},
		{"JSON", "application/json", `{"status":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source, upstream := io.Pipe()
			defer func() { _ = upstream.Close() }()
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: source}
			defer func() { _ = resp.Body.Close() }()
			go func() { _, _ = io.WriteString(upstream, tc.prefix) }()
			result := probeOpenAICollaborationResponseWithWait(context.Background(), resp, 10*time.Second)
			require.Empty(t, result.Failure)
			require.Equal(t, "prelude_deadline", result.Reason)
			require.False(t, result.BlankPrelude)
			require.GreaterOrEqual(t, result.Elapsed, 2*time.Second)
			require.Less(t, result.Elapsed, 4*time.Second)
			_ = upstream.Close()
			restored, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tc.prefix, string(restored))
		})
	}
}

func TestCollaborationProbeBlankWaitIsAbsolute(t *testing.T) {
	t.Parallel()
	source, upstream := io.Pipe()
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: source}
	defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
	stop := make(chan struct{})
	written := make(chan string, 1)
	go func() {
		var body strings.Builder
		write := func(frame string) { _, _ = body.WriteString(frame); _, _ = io.WriteString(upstream, frame) }
		write(collaborationOpenBlankPrelude())
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				write(": heartbeat\n\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"item_id\":\"msg_placeholder\",\"delta\":\" \"}\n\n")
			case <-stop:
				write(collaborationProbePlaceholderFailed)
				_ = upstream.Close()
				written <- body.String()
				return
			}
		}
	}()
	result := probeOpenAICollaborationResponseWithWait(context.Background(), resp, 3*time.Second)
	close(stop)
	require.Empty(t, result.Failure)
	require.True(t, result.BlankPrelude)
	require.Equal(t, "prelude_deadline", result.Reason)
	require.GreaterOrEqual(t, result.Elapsed, 3*time.Second)
	require.Less(t, result.Elapsed, 5*time.Second, "whitespace must not renew the deadline")
	restored, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, <-written, string(restored))
}

func TestCollaborationProbeExtendedWaitReleasesOnContent(t *testing.T) {
	for _, tc := range []struct{ name, frame, reason string }{
		{"text", `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":"hello"}`, "output_or_unknown_event"},
		{"tool", `{"type":"response.output_item.added","item":{"type":"function_call"}}`, "output_or_unknown_event"},
		{"reasoning", `{"type":"response.reasoning_text.delta","delta":"thinking"}`, "output_or_unknown_event"},
		{"unknown", `{"type":"provider.unknown"}`, "output_or_unknown_event"},
		{"normal blank completion", `{"type":"response.completed","response":{"status":"completed","output":[]}}`, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source, upstream := io.Pipe()
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: source}
			defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
			body := collaborationOpenBlankPrelude() + "data: " + tc.frame + "\n\n"
			go func() {
				_, _ = io.WriteString(upstream, collaborationOpenBlankPrelude())
				time.Sleep(2200 * time.Millisecond)
				_, _ = io.WriteString(upstream, "data: "+tc.frame+"\n\n")
			}()
			result := probeOpenAICollaborationResponseWithWait(context.Background(), resp, 10*time.Second)
			require.Empty(t, result.Failure)
			require.Equal(t, tc.reason, result.Reason)
			require.GreaterOrEqual(t, result.Elapsed, 2200*time.Millisecond)
			require.Less(t, result.Elapsed, 4*time.Second)
			_ = upstream.Close()
			restored, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, body, string(restored))
		})
	}
}

func TestCollaborationProbeExtendedWaitHonorsCancellationAndSize(t *testing.T) {
	t.Run("existing response deadline", func(t *testing.T) {
		source, upstream := io.Pipe()
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: source}
		defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
		ctx := context.Background()
		go func() { _, _ = io.WriteString(upstream, collaborationOpenBlankPrelude()) }()
		result := probeOpenAICollaborationResponseWithWait(ctx, resp, 10*time.Second, time.Now().Add(200*time.Millisecond))
		require.Empty(t, result.Failure)
		require.Equal(t, "response_deadline", result.Reason)
		require.Less(t, result.Elapsed, time.Second)
		require.NoError(t, ctx.Err())
		go func() { _, _ = io.WriteString(upstream, collaborationProbePlaceholderFailed); _ = upstream.Close() }()
		restored, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, collaborationOpenBlankPrelude()+collaborationProbePlaceholderFailed, string(restored))
	})
	t.Run("request deadline", func(t *testing.T) {
		source, upstream := io.Pipe()
		tracked := &collaborationProbeTrackedSource{ReadCloser: source, started: make(chan struct{}), completed: make(chan struct{})}
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: tracked}
		defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		go func() { _, _ = io.WriteString(upstream, collaborationOpenBlankPrelude()) }()
		result := probeOpenAICollaborationResponseWithWait(ctx, resp, 10*time.Second)
		require.Empty(t, result.Failure)
		require.Equal(t, "canceled", result.Reason)
		require.Less(t, result.Elapsed, time.Second)
		select {
		case <-tracked.completed:
		case <-time.After(time.Second):
			t.Fatal("cancellation did not stop the upstream reader")
		}
	})
	t.Run("byte limit", func(t *testing.T) {
		body := collaborationOpenBlankPrelude() + ":" + strings.Repeat(" ", openAICollaborationPreludeBytes) + "\n\n" + collaborationProbePlaceholderFailed
		resp := collaborationProbeResponse("text/event-stream", body)
		defer func() { _ = resp.Body.Close() }()
		result := probeOpenAICollaborationResponseWithWait(context.Background(), resp, 10*time.Second)
		require.Empty(t, result.Failure)
		require.Equal(t, "prelude_size_limit", result.Reason)
		restored, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(restored))
	})
}
