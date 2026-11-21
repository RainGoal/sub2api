package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type openAIWSDialAuditCapture struct {
	parts      map[string]requestaudit.PartManifest
	headers    http.Header
	response   http.Header
	body       map[string][]byte
	status     int
	ended      int
	aborted    int
	endReasons map[string]string
}

func newOpenAIWSDialAuditCapture() *openAIWSDialAuditCapture {
	return &openAIWSDialAuditCapture{
		parts:      make(map[string]requestaudit.PartManifest),
		headers:    make(http.Header),
		response:   make(http.Header),
		body:       make(map[string][]byte),
		endReasons: make(map[string]string),
	}
}

func (c *openAIWSDialAuditCapture) Enabled(part string) bool { return true }

func (c *openAIWSDialAuditCapture) AddRequestHeader(name string, values []string) {
	for _, value := range values {
		c.headers.Add(name, value)
	}
}

func (c *openAIWSDialAuditCapture) EndRequestHeaders() { c.ended++ }

func (c *openAIWSDialAuditCapture) AbortRequestHeaders() { c.aborted++ }

func (c *openAIWSDialAuditCapture) CaptureHeaders(_ string, headers http.Header) {
	c.response = headers.Clone()
	c.parts[requestaudit.UpstreamResponseHeaders] = requestaudit.PartManifest{Complete: true}
}

func (c *openAIWSDialAuditCapture) SetStatus(status int) { c.status = status }

func (c *openAIWSDialAuditCapture) EndPart(part string, complete bool, reason string) {
	c.parts[part] = requestaudit.PartManifest{Complete: complete, Reason: reason}
	c.endReasons[part] = reason
}

func (c *openAIWSDialAuditCapture) WrapBody(part string, body io.ReadCloser) io.ReadCloser {
	data, _ := io.ReadAll(body)
	_ = body.Close()
	c.body[part] = data
	c.parts[part] = requestaudit.PartManifest{Complete: true}
	return io.NopCloser(bytes.NewReader(data))
}

func TestDialOpenAIWSWithAuditCapturesActualHandshakeHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer raw-token", r.Header.Get("Authorization"))
		require.Equal(t, "Upgrade", r.Header.Get("Connection"))
		require.Equal(t, "websocket", r.Header.Get("Upgrade"))
		require.NotEmpty(t, r.Header.Get("Sec-WebSocket-Key"))
		w.Header().Set("X-Upstream-Handshake", "accepted")
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{InsecureSkipVerify: true})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.CloseNow() })
	}))
	defer server.Close()

	ctx := context.Background()
	capture := newOpenAIWSDialAuditCapture()
	var headers http.Header
	conn, status, headers, err := dialOpenAIWSWithAudit(ctx, capture, func(dialCtx context.Context) (openAIWSClientConn, int, http.Header, error) {
		return newDefaultOpenAIWSClientDialer().Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"),
			http.Header{"Authorization": {"Bearer raw-token"}, "X-Multi": {"one", "two"}}, "")
	})
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer func() { _ = conn.Close() }()
	require.Equal(t, http.StatusSwitchingProtocols, status)
	require.Equal(t, "accepted", headers.Get("X-Upstream-Handshake"))
	require.Equal(t, "Bearer raw-token", capture.headers.Get("Authorization"))
	require.Equal(t, []string{"one", "two"}, capture.headers.Values("X-Multi"))
	require.Equal(t, "Upgrade", capture.headers.Get("Connection"))
	require.Equal(t, "websocket", capture.headers.Get("Upgrade"))
	require.NotEmpty(t, capture.headers.Get("Sec-WebSocket-Key"))
	require.Equal(t, "accepted", capture.response.Get("X-Upstream-Handshake"))
	require.Equal(t, 101, capture.status)
	require.Equal(t, 1, capture.ended)
	require.Equal(t, "protocol_upgrade", capture.endReasons[requestaudit.UpstreamRequestBody])
	require.Equal(t, "protocol_upgrade", capture.endReasons[requestaudit.UpstreamResponseBody])
}

func TestDialOpenAIWSWithAuditDoesNotInventHeadersForUntracedDialer(t *testing.T) {
	capture := newOpenAIWSDialAuditCapture()
	conn, status, headers, err := dialOpenAIWSWithAudit(context.Background(), capture, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
		return &coderOpenAIWSClientConn{}, http.StatusSwitchingProtocols, make(http.Header), nil
	})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, http.StatusSwitchingProtocols, status)
	require.Empty(t, headers)
	require.Empty(t, capture.headers)
	require.Equal(t, 0, capture.ended)
	require.Equal(t, "not_observed", capture.endReasons[requestaudit.UpstreamRequestHeaders])
}
