package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const collaborationProbeCreated = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_before\",\"output\":[]}}\n\n"
const collaborationProbeFailed = "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"invalid_encrypted_content\",\"message\":\"Cannot decrypt agent assignment\"},\"usage\":{\"input_tokens\":12}}}\n\n"
const collaborationProbeBareError = "event: error\ndata: {\"error\":{\"code\":\"invalid_encrypted_content\",\"message\":\"Cannot decrypt agent assignment\"}}\n\n"
const collaborationProbeCompleted = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"

func collaborationProbeResponse(contentType, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestCollaborationProbeSSEFailureAndTerminalPrecedence(t *testing.T) {
	for _, test := range []struct {
		name        string
		body        string
		wantFailure bool
	}{
		{"created then early failure", collaborationProbeCreated + collaborationProbeFailed, true},
		{"bare error then failed", collaborationProbeBareError + collaborationProbeFailed, true},
		{"bare error then completed", collaborationProbeBareError + collaborationProbeCompleted, false},
		{"bare error then EOF", collaborationProbeBareError, true},
		{"bare error then done", collaborationProbeBareError + "data: [DONE]\n\n", true},
		{"no terminal EOF", collaborationProbeCreated, false},
		{"partial error EOF", strings.TrimSuffix(collaborationProbeBareError, "\n\n"), false},
		{"ordinary failure", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"rate_limit_exceeded\"}}}\n\n", false},
		{"text commits", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" + collaborationProbeFailed, false},
		{"tool commits", "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\"}}\n\n" + collaborationProbeFailed, false},
		{"hosted tool commits", "data: {\"type\":\"response.web_search_call.in_progress\"}\n\n" + collaborationProbeFailed, false},
		{"unknown event commits", "event: provider.new_event\ndata: {\"value\":1}\n\n" + collaborationProbeFailed, false},
		{"failed with output commits", "data: {\"type\":\"response.failed\",\"response\":{\"output\":[{\"type\":\"web_search_call\"}],\"error\":{\"code\":\"invalid_encrypted_content\"}}}\n\n", false},
		{"bare error with output commits", "event: error\ndata: {\"output\":[{\"type\":\"web_search_call\"}],\"error\":{\"code\":\"invalid_encrypted_content\"}}\n\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := collaborationProbeResponse("text/event-stream", test.body)
			defer func() { _ = resp.Body.Close() }()
			failure := probeOpenAICollaborationResponse(context.Background(), resp)
			require.Equal(t, test.wantFailure, len(failure) > 0)
			restored, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, test.body, string(restored), "probe must preserve the original stream byte-for-byte")
		})
	}
}

func TestCollaborationProbeSSERejectsNonRecoverableHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		for _, body := range []string{collaborationProbeFailed, collaborationProbeBareError} {
			resp := collaborationProbeResponse("text/event-stream", body)
			resp.StatusCode = status
			failure := probeOpenAICollaborationResponse(context.Background(), resp)
			restored, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			require.Empty(t, failure, "HTTP %d must not authorize recovery", status)
			require.NoError(t, err)
			require.Equal(t, body, string(restored))
		}
	}
}

func TestCollaborationProbeMultilineDataAndEventName(t *testing.T) {
	body := "event: response.failed\r\ndata: {\"response\":\r\ndata: {\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"invalid_encrypted_content\"}}}\r\n\r\n"
	resp := collaborationProbeResponse("text/event-stream", body)
	defer func() { _ = resp.Body.Close() }()
	failure := probeOpenAICollaborationResponse(context.Background(), resp)
	require.Equal(t, "response.failed", gjson.GetBytes(failure, "type").String())
	require.Equal(t, "invalid_encrypted_content", gjson.GetBytes(failure, "response.error.code").String())
	restored, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(restored))
}

func TestCollaborationProbeJSONFailureAndSideEffectBoundary(t *testing.T) {
	for _, test := range []struct {
		name, body  string
		status      int
		wantFailure bool
	}{
		{"failed response", `{"status":"failed","output":[],"error":{"code":"invalid_encrypted_content"}}`, 200, true},
		{"HTTP error", `{"error":{"code":"invalid_encrypted_content"}}`, 400, true},
		{"successful response", `{"status":"completed","error":{"code":"invalid_encrypted_content"}}`, 200, false},
		{"tool output", `{"status":"failed","output":[{"type":"web_search_call"}],"error":{"code":"invalid_encrypted_content"}}`, 200, false},
		{"wrapped output", `{"type":"response.failed","response":{"output":[{"type":"function_call"}],"error":{"code":"invalid_encrypted_content"}}}`, 200, false},
		{"rate limit", `{"error":{"code":"invalid_encrypted_content"}}`, 429, false},
		{"truncated JSON", `{"error":{"code":"invalid_encrypted_content"}`, 400, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := collaborationProbeResponse("application/json", test.body)
			resp.StatusCode = test.status
			defer func() { _ = resp.Body.Close() }()
			failure := probeOpenAICollaborationResponse(context.Background(), resp)
			require.Equal(t, test.wantFailure, len(failure) > 0)
			restored, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, test.body, string(restored))
		})
	}
}

func TestCollaborationProbeDeadlineCannotRecoverLateFailure(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			t.Parallel()
			source, upstream := io.Pipe()
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: source}
			defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
			result := make(chan []byte, 1)
			started := time.Now()
			go func() { result <- probeOpenAICollaborationResponse(context.Background(), resp) }()
			select {
			case failure := <-result:
				require.Empty(t, failure)
				require.GreaterOrEqual(t, time.Since(started), openAICollaborationPreludeWait)
			case <-time.After(openAICollaborationPreludeWait + 2*time.Second):
				t.Fatal("probe remained blocked after its deadline")
			}
			body := collaborationProbeFailed
			if contentType == "application/json" {
				body = `{"status":"failed","output":[],"error":{"code":"invalid_encrypted_content"}}`
			}
			go func() { _, _ = io.WriteString(upstream, body); _ = upstream.Close() }()
			restored, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, body, string(restored), "a late error must be delivered through the original response")
		})
	}
}

func TestCollaborationProbeSizeLimitReleasesBeforeEOF(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			source, upstream := io.Pipe()
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: source}
			defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
			prefix := strings.Repeat("x", openAICollaborationPreludeBytes+1)
			go func() { _, _ = io.WriteString(upstream, prefix) }()
			result := make(chan []byte, 1)
			go func() { result <- probeOpenAICollaborationResponse(context.Background(), resp) }()
			select {
			case failure := <-result:
				require.Empty(t, failure)
			case <-time.After(time.Second):
				t.Fatal("probe did not release an over-limit unterminated line before EOF")
			}
			go func() { _, _ = io.WriteString(upstream, collaborationProbeFailed); _ = upstream.Close() }()
			restored, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, prefix+collaborationProbeFailed, string(restored))
		})
	}
}

type collaborationProbeTrackedSource struct {
	io.ReadCloser
	started   chan struct{}
	completed chan struct{}
	once      sync.Once
	mu        sync.Mutex
	closes    int
}

func (s *collaborationProbeTrackedSource) Read(p []byte) (int, error) {
	s.once.Do(func() { close(s.started) })
	return s.ReadCloser.Read(p)
}

func (s *collaborationProbeTrackedSource) Close() error {
	err := s.ReadCloser.Close()
	s.mu.Lock()
	s.closes++
	if s.closes == 2 {
		// The wrapper and worker both close the source. The second close
		// proves cancellation released the worker's deferred cleanup.
		close(s.completed)
	}
	s.mu.Unlock()
	return err
}

func TestCollaborationProbeCancellationStopsReader(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			source, upstream := io.Pipe()
			tracked := &collaborationProbeTrackedSource{ReadCloser: source, started: make(chan struct{}), completed: make(chan struct{})}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: tracked}
			defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan []byte, 1)
			go func() { result <- probeOpenAICollaborationResponse(ctx, resp) }()
			select {
			case <-tracked.started:
			case <-time.After(time.Second):
				t.Fatal("probe did not start reading")
			}
			cancel()
			select {
			case failure := <-result:
				require.Empty(t, failure)
			case <-time.After(time.Second):
				t.Fatal("probe did not honor cancellation")
			}
			select {
			case <-tracked.completed:
			case <-time.After(time.Second):
				t.Fatal("probe reader goroutine remained blocked after cancellation")
			}
		})
	}
}

func TestCollaborationProbeDeadlineFlushesBufferedPrefix(t *testing.T) {
	for _, tc := range []struct{ name, contentType, prefix string }{
		{"SSE lifecycle", "text/event-stream", collaborationProbeCreated},
		{"SSE blank placeholder", "text/event-stream", collaborationProbeWhitespacePrelude},
		{"JSON", "application/json", `{"status":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source, upstream := io.Pipe()
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {tc.contentType}}, Body: source}
			defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
			prefix := tc.prefix
			result := make(chan []byte, 1)
			go func() { result <- probeOpenAICollaborationResponse(context.Background(), resp) }()
			written := make(chan struct{})
			go func() { _, _ = io.WriteString(upstream, prefix); close(written) }()
			select {
			case <-written:
			case <-time.After(time.Second):
				t.Fatal("probe did not consume the initial upstream bytes")
			}
			select {
			case failure := <-result:
				require.Empty(t, failure)
			case <-time.After(openAICollaborationPreludeWait + 2*time.Second):
				t.Fatal("probe remained blocked after its deadline")
			}
			type readResult struct {
				body string
				err  error
			}
			read := make(chan readResult, 1)
			go func() {
				buffer := make([]byte, len(prefix))
				_, err := io.ReadFull(resp.Body, buffer)
				read <- readResult{string(buffer), err}
			}()
			select {
			case got := <-read:
				require.NoError(t, got.err)
				require.Equal(t, prefix, got.body)
			case <-time.After(time.Second):
				t.Fatal("deadline did not release buffered bytes while upstream was stalled")
			}
		})
	}
}
