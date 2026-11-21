package repository

import (
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type auditHeaderTestCapture struct {
	fields    http.Header
	completed int
	aborted   int
	reason    string
}

func (c *auditHeaderTestCapture) AddRequestHeader(name string, values []string) {
	for _, value := range values {
		c.fields.Add(name, value)
	}
}

func (c *auditHeaderTestCapture) EndRequestHeaders() { c.completed++ }

func (c *auditHeaderTestCapture) AbortRequestHeaders() { c.aborted++ }

func (c *auditHeaderTestCapture) EndPart(_ string, _ bool, reason string) { c.reason = reason }

func TestRequestAuditWrittenHeadersMatchesHTTPTransport(t *testing.T) {
	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer synthetic-token")
	req.Header["X-Value"] = []string{"first", "second"}
	var originalTraceCalled atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		WroteHeaders: func() { originalTraceCalled.Store(true) },
	}))
	collector := &auditHeaderTestCapture{fields: make(http.Header)}
	headers := &requestAuditWrittenHeaders{attempt: collector}
	resp, err := server.Client().Do(headers.trace(req))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	headers.finishAfterRoundTrip(req, true)
	headers.add("X-Late", []string{"ignored"})
	headers.finish()
	actual := <-seen
	require.Equal(t, actual.Values("X-Value"), collector.fields.Values("X-Value"))
	require.Equal(t, "Bearer synthetic-token", collector.fields.Get("Authorization"))
	require.Equal(t, actual.Get("Accept-Encoding"), collector.fields.Get("Accept-Encoding"))
	require.NotEmpty(t, collector.fields.Get("Accept-Encoding"))
	require.Equal(t, req.Host, collector.fields.Get("Host"))
	require.Empty(t, collector.fields.Get("X-Late"))
	require.Equal(t, 1, collector.completed)
	require.True(t, originalTraceCalled.Load())
}

func TestRequestAuditWrittenHeadersFallbackAndError(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
	require.NoError(t, err)
	req.Header.Set("X-Raw", "original")
	collector := &auditHeaderTestCapture{fields: make(http.Header)}
	headers := &requestAuditWrittenHeaders{attempt: collector}
	headers.finishAfterRoundTrip(req, true)
	headers.finishAfterRoundTrip(req, true)
	require.Equal(t, 1, collector.completed)
	require.Equal(t, []string{"original"}, collector.fields.Values("X-Raw"))
	require.Equal(t, "example.test", collector.fields.Get("Host"))

	collector = &auditHeaderTestCapture{fields: make(http.Header)}
	headers = &requestAuditWrittenHeaders{attempt: collector}
	headers.add("X-Limited", []string{"partial"})
	headers.finishAfterRoundTrip(req, false)
	require.Equal(t, 1, collector.aborted)
	require.Equal(t, "partial", collector.fields.Get("X-Limited"))
	require.Empty(t, collector.fields.Get("X-Raw"))
}
