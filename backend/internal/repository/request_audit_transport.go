package repository

import (
	"net/http"
	"net/http/httptrace"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
)

type requestAuditTransport struct {
	base      http.RoundTripper
	accountID int64
}

func (t *requestAuditTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return t.base.RoundTrip(req)
	}
	attempt := requestaudit.BeginUpstreamAttempt(req.Context(), req.Method, req.URL.Host, req.URL.EscapedPath(), t.accountID)
	if attempt == nil {
		return t.base.RoundTrip(req)
	}

	forward := req
	var requestHeaders *requestAuditWrittenHeaders
	if attempt.Enabled(requestaudit.UpstreamRequestHeaders) {
		requestHeaders = &requestAuditWrittenHeaders{attempt: attempt}
		forward = requestHeaders.trace(req)
	}
	if attempt.Enabled(requestaudit.UpstreamRequestBody) {
		if req.Body == nil || req.Body == http.NoBody {
			attempt.EndPart(requestaudit.UpstreamRequestBody, true, "")
		} else {
			if forward == req {
				forward = req.WithContext(req.Context())
			}
			forward.Body = attempt.WrapBody(requestaudit.UpstreamRequestBody, req.Body)
		}
	}

	resp, err := t.base.RoundTrip(forward)
	if requestHeaders != nil {
		requestHeaders.finishAfterRoundTrip(req, resp != nil && err == nil)
	}
	if resp == nil {
		attempt.EndPart(requestaudit.UpstreamResponseHeaders, false, "no_response")
		attempt.EndPart(requestaudit.UpstreamResponseBody, false, "no_response")
		return resp, err
	}
	if err != nil {
		attempt.EndPart(requestaudit.UpstreamResponseHeaders, false, "transport_error")
		attempt.EndPart(requestaudit.UpstreamResponseBody, false, "transport_error")
		return resp, err
	}
	attempt.SetStatus(resp.StatusCode)
	attempt.CaptureHeaders(requestaudit.UpstreamResponseHeaders, resp.Header)
	if attempt.Enabled(requestaudit.UpstreamResponseBody) {
		switch {
		case resp.StatusCode == http.StatusSwitchingProtocols:
			attempt.EndPart(requestaudit.UpstreamResponseBody, false, "protocol_upgrade")
		case resp.Body == nil || resp.Body == http.NoBody:
			attempt.EndPart(requestaudit.UpstreamResponseBody, true, "")
		default:
			resp.Body = attempt.WrapBody(requestaudit.UpstreamResponseBody, resp.Body)
		}
	}
	return resp, nil
}

// Keep each RoundTrip's first complete encoded header set. Late transport
// callbacks and internal connection retries cannot append it a second time.
type requestAuditWrittenHeaders struct {
	mu       sync.Mutex
	attempt  requestAuditHeaderCapture
	observed bool
	finished bool
}

type requestAuditHeaderCapture interface {
	AddRequestHeader(string, []string)
	EndRequestHeaders()
	AbortRequestHeaders()
	EndPart(string, bool, string)
}

func (h *requestAuditWrittenHeaders) trace(req *http.Request) *http.Request {
	return req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		WroteHeaderField: h.add,
		WroteHeaders:     h.finish,
	}))
}

func (h *requestAuditWrittenHeaders) add(name string, values []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.finished {
		h.observed = true
		h.attempt.AddRequestHeader(name, values)
	}
}

func (h *requestAuditWrittenHeaders) finish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.finished && h.observed {
		h.finished = true
		h.attempt.EndRequestHeaders()
	}
}

func (h *requestAuditWrittenHeaders) finishAfterRoundTrip(req *http.Request, succeeded bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.finished {
		return
	}
	h.finished = true
	if !succeeded {
		if h.observed {
			h.attempt.AbortRequestHeaders()
		} else {
			h.attempt.EndPart(requestaudit.UpstreamRequestHeaders, false, "not_written")
		}
		return
	}
	if h.observed {
		h.attempt.EndRequestHeaders()
		return
	}
	// A custom RoundTripper can succeed without emitting transport callbacks.
	for name, values := range req.Header {
		h.attempt.AddRequestHeader(name, values)
	}
	if _, found := req.Header["Host"]; !found {
		host := req.Host
		if host == "" {
			host = req.URL.Host
		}
		if host != "" {
			h.attempt.AddRequestHeader("Host", []string{host})
		}
	}
	h.attempt.EndRequestHeaders()
}
