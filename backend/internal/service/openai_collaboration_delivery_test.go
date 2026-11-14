package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type collaborationTerminalFailingWriter struct {
	gin.ResponseWriter
	failedWrites int
}

func (w *collaborationTerminalFailingWriter) Write(payload []byte) (int, error) {
	if strings.Contains(string(payload), "response.completed") {
		w.failedWrites++
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(payload)
}

func (w *collaborationTerminalFailingWriter) WriteString(payload string) (int, error) {
	return w.Write([]byte(payload))
}

func TestCollaborationCompletedButUndeliveredDoesNotRemember(t *testing.T) {
	for _, transport := range []string{"http", "passthrough", "ws_bridge"} {
		t.Run(transport, func(t *testing.T) {
			body := []byte(strings.Replace(collaborationMixedRequest, `"model":"gpt-5"`, `"model":"gpt-5","stream":true`, 1))
			success := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + collaborationCompletedJSON + "}\n\n"
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				collaborationTestResponse(400, collaborationFailedJSON, false),
				collaborationTestResponse(200, success, true),
				collaborationTestResponse(200, success, true),
			}}
			svc := configureCollaborationTestService(t, upstream, true)
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_passthrough"] = transport == "passthrough"
			c := newCollaborationTestContext(body, 11)
			writer := &collaborationTerminalFailingWriter{ResponseWriter: c.Writer}
			c.Writer = writer
			var result *OpenAIForwardResult
			var err error
			if transport == "ws_bridge" {
				result, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "test-token", body, len(body), "gpt-5", "", "", "", "", 1, func(payload []byte) error {
					if strings.Contains(string(payload), "response.completed") {
						writer.failedWrites++
						return io.EOF
					}
					return nil
				})
			} else {
				result, err = svc.Forward(context.Background(), c, account, body)
			}
			require.NoError(t, err, "disconnect must preserve the existing usage-draining result")
			require.NotNil(t, result)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			require.Positive(t, writer.failedWrites, "the terminal event must fail at the downstream writer")
			require.Len(t, upstream.bodies, 2)
			require.NotContains(t, string(upstream.bodies[1]), "old-opaque", "the first attempt must have actually recovered")
			attempt := collaborationAttempt(c)
			require.True(t, attempt.completed, "the upstream completed even though delivery failed")
			require.True(t, attempt.deliveryFailed)
			require.Empty(t, svc.collaborationCache.read(attempt.scope+":"+attempt.branch).omissions)
			// A fresh request with the same authenticated scope and branch must
			// still send the opaque history: a failed delivery is not authority
			// to omit it proactively in later requests.
			next := newCollaborationTestContext(body, 11)
			if transport == "ws_bridge" {
				_, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), next, account, "test-token", body, len(body), "gpt-5", "", "", "", "", 1, func([]byte) error { return nil })
			} else {
				_, err = svc.Forward(context.Background(), next, account, body)
			}
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 3)
			require.Contains(t, string(upstream.bodies[2]), "old-opaque")
		})
	}
}
