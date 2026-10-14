package requestaudit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func captureTestManager(options ...Option) *Manager {
	m := NewManager(nil, options...)
	m.config.Enabled, m.configKnown, m.running = true, true, true
	m.queue = make(chan auditEvent, m.maxEvents)
	return m
}

func takeEvents(m *Manager) []auditEvent {
	var events []auditEvent
	for len(m.queue) > 0 {
		events = append(events, <-m.queue)
	}
	return events
}

func TestCapturePreservesRawBytesAndMultiValueHeaders(t *testing.T) {
	m := captureTestManager()
	headers := http.Header{"X-Multi": {"first", "second"}, "Authorization": {"Bearer original"}}
	body := append(bytes.Repeat([]byte{0xff, 0, '\n'}, ChunkBytes/3), []byte(`{"a":1,"a":2}`)...)
	c := m.Begin(RequestMeta{Headers: headers, Host: "test.example", RequestURI: "/v1/messages?a=%2F&a=2", ContentLength: int64(len(body))})
	require.NotNil(t, c)
	headers.Set("X-Multi", "mutated")
	trailers := http.Header{"X-Checksum": {"final", "second"}}
	r := c.WrapBodyWithTrailers(io.NopCloser(bytes.NewReader(body)), func() http.Header { return trailers })
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, body, got)
	beforeFinish := takeEvents(m)
	var rawBody, rawHeaders []byte
	ended := make(map[string]bool)
	for _, event := range beforeFinish {
		if event.kind == "chunk" && event.part == PartBody {
			rawBody = append(rawBody, event.raw...)
		}
		if event.kind == "chunk" && event.part == PartHeaders {
			rawHeaders = append(rawHeaders, event.raw...)
		}
		if event.kind == "part_end" {
			ended[event.part] = event.manifest.Complete
		}
	}
	require.Equal(t, body, rawBody)
	require.Equal(t, map[string]bool{PartHeaders: true, PartBody: true, PartTrailers: true}, ended, "capture finishes before a long response completes")
	var envelope struct {
		Headers http.Header `json:"headers"`
		Host    string      `json:"host"`
		URI     string      `json:"request_uri"`
	}
	require.NoError(t, json.Unmarshal(rawHeaders, &envelope))
	require.Equal(t, []string{"first", "second"}, envelope.Headers["X-Multi"])
	require.Equal(t, "test.example", envelope.Host)
	require.Equal(t, "/v1/messages?a=%2F&a=2", envelope.URI)
	c.Complete(Completion{HTTPStatus: 200})
	c.Finish()
	c.Finish()
	events := takeEvents(m)
	require.Len(t, events, 1)
	require.Equal(t, "complete", events[0].captureState)
	m.releaseEvents(append(beforeFinish, events...))
	require.Zero(t, m.queueBytes.Load())
	require.Zero(t, m.queueEvents.Load())
}

func TestCaptureNeverInfersEOFfromContentLength(t *testing.T) {
	m := captureTestManager()
	c := m.Begin(RequestMeta{ContentLength: 0})
	c.Finish()
	events := takeEvents(m)
	last := events[len(events)-1]
	require.Equal(t, "partial", last.captureState)
	require.False(t, last.manifests[PartBody].Complete)
	c = m.Begin(RequestMeta{})
	require.Equal(t, http.NoBody, c.WrapBody(http.NoBody))
	c.Finish()
	events = takeEvents(m)
	require.True(t, events[len(events)-1].manifests[PartBody].Complete)
}

func TestCapturePressureNeverResumesAcrossAGap(t *testing.T) {
	m := captureTestManager(WithMaxEvents(8), WithBufferBytes(2*1024*1024))
	c := m.Begin(RequestMeta{})
	c.Append(PartBody, bytes.Repeat([]byte{1}, 6*ChunkBytes))
	require.True(t, c.dropped)
	events := takeEvents(m)
	m.releaseEvents(events)
	c.Append(PartBody, []byte("must not resume"))
	require.Empty(t, m.queue)
	c.EndPart(PartBody, true, "")
	c.Finish()
	events = takeEvents(m)
	last := events[len(events)-1]
	require.Equal(t, "partial", last.captureState)
	require.EqualValues(t, 6*ChunkBytes+len("must not resume"), last.observedBodyBytes)
	require.False(t, last.manifests[PartBody].Complete)
	m.releaseEvents(events)
	require.Zero(t, m.queueBytes.Load())
}

func TestBeginRejectsFullBudgetWithoutLeakingEnvelopeReservation(t *testing.T) {
	for _, limit := range []string{"bytes", "events"} {
		t.Run(limit, func(t *testing.T) {
			m := captureTestManager(WithMaxEvents(1), WithBufferBytes(1024*1024))
			if limit == "bytes" {
				require.True(t, m.reserve(m.bufferBytes, false))
			} else {
				require.True(t, m.enqueue(auditEvent{kind: "bind"}))
			}
			used, events := m.queueBytes.Load(), m.queueEvents.Load()
			c := m.Begin(RequestMeta{Headers: http.Header{"X-Envelope": {string(bytes.Repeat([]byte{0}, 4096))}}})
			require.Nil(t, c)
			require.Equal(t, used, m.queueBytes.Load())
			require.Equal(t, events, m.queueEvents.Load())
			require.EqualValues(t, 1, m.droppedRequests.Load())
			if limit == "bytes" {
				m.queueBytes.Add(-used)
			} else {
				m.releaseEvents(takeEvents(m))
			}
			require.Zero(t, m.queueBytes.Load())
		})
	}
}

func TestTrailersBudgetFailureMarksIncompleteWithoutLeaking(t *testing.T) {
	m := captureTestManager(WithBufferBytes(1024 * 1024))
	c := m.Begin(RequestMeta{})
	require.NotNil(t, c)
	m.releaseEvents(takeEvents(m))
	pressure := m.bufferBytes - 2*eventOverhead
	require.True(t, m.reserve(pressure, false))
	c.Trailers(http.Header{"X-Trailer": {"value"}})
	require.False(t, c.manifests[PartTrailers].Complete)
	require.Equal(t, "queue_full", c.manifests[PartTrailers].Reason)
	events := takeEvents(m)
	require.Len(t, events, 1, "reserved control capacity records the incomplete part")
	m.releaseEvents(events)
	require.Equal(t, pressure, m.queueBytes.Load())
	m.queueBytes.Add(-pressure)
	require.Zero(t, m.queueBytes.Load())
}

func TestIngressIdentityIsIndependentAndKeepsOriginalKey(t *testing.T) {
	m := NewManager(nil)
	i := BeginIngress(m, RequestMeta{})
	require.Nil(t, i.Capture())
	require.True(t, i.SetSession("", "header", "invalid"))
	i.BindIdentity(1, 2, 3)
	i.BindIdentity(4, 5, 6)
	require.True(t, i.SetSession("original", "header", "resolved"))
	require.False(t, i.SetSession("rewritten", "body", "resolved"))
	require.Equal(t, Identity{UserID: 1, APIKeyID: 2, GroupID: 3, SessionID: "original", SessionSource: "header", SessionResolution: "resolved"}, i.Identity())
}

func TestIngressConcurrentBindingKeepsCompleteIdentity(t *testing.T) {
	m := captureTestManager()
	i := BeginIngress(m, RequestMeta{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); i.BindIdentity(1, 2, 3) }()
	go func() { defer wg.Done(); i.SetSession("client", "header", "resolved") }()
	wg.Wait()
	i.capture.Finish()
	events := takeEvents(m)
	last := events[len(events)-1]
	require.Equal(t, i.Identity(), *last.identity)
	require.EqualValues(t, 2, last.identity.APIKeyID)
	require.Equal(t, "client", last.identity.SessionID)
}

func TestWebSocketSnapshotBudgetIsReleasedAndOldConnectionsStayUncaptured(t *testing.T) {
	m := captureTestManager()
	meta := RequestMeta{Kind: "websocket_handshake", Headers: http.Header{"Authorization": {"original"}}}
	i := BeginIngress(m, meta)
	initial := takeEvents(m)
	m.releaseEvents(initial)
	require.Positive(t, i.snapshotBytes)
	require.Equal(t, i.snapshotBytes, m.queueBytes.Load())
	meta.Headers.Set("Authorization", "mutated")
	i.RecordMessage("binary", []byte{0xff, 0})
	events := takeEvents(m)
	var headers []byte
	for _, e := range events {
		if e.part == PartHeaders && e.kind == "chunk" {
			headers = append(headers, e.raw...)
		}
	}
	require.Contains(t, string(headers), `"header_source":"handshake"`)
	require.Contains(t, string(headers), `"Authorization":["original"]`)
	m.releaseEvents(events)
	i.Finish(101, time.Now(), "completed", nil)
	m.releaseEvents(takeEvents(m))
	require.Zero(t, m.queueBytes.Load())
	require.Nil(t, i.meta.Headers)
	i.Finish(101, time.Now(), "completed", nil)
	require.Zero(t, m.queueBytes.Load())
	m.config.Enabled = false
	old := BeginIngress(m, meta)
	require.Nil(t, old.meta.Headers)
	m.config.Enabled = true
	old.RecordMessage("text", []byte("after enabling"))
	require.Empty(t, m.queue)
}

func TestWebSocketFirstMessageUsesIdentityResolvedAfterHandshake(t *testing.T) {
	m := captureTestManager()
	i := BeginIngress(m, RequestMeta{Kind: "websocket_handshake"})
	i.BindIdentity(1, 2, 3)
	require.True(t, i.SetSession("late-route-session", "x-grok-conv-id", "resolved"))
	i.SetModel("grok-original")
	raw := []byte(" {\"model\":\"grok-original\",\"value\":1,\"value\":2} \n")
	i.RecordMessage("text", raw)
	i.Finish(101, time.Now(), "completed", nil)
	events := takeEvents(m)
	defer m.releaseEvents(events)
	var messageID string
	for _, event := range events {
		if event.kind == "begin" && event.meta.Kind == "websocket_message" {
			messageID = event.id
			require.EqualValues(t, 1, event.meta.MessageSeq)
		}
	}
	require.NotEmpty(t, messageID)
	var captured []byte
	var bound, finished bool
	for _, event := range events {
		if event.id != messageID {
			continue
		}
		switch event.kind {
		case "bind", "finish":
			require.NotNil(t, event.identity)
			require.Equal(t, i.Identity(), *event.identity)
			bound = bound || event.kind == "bind"
			finished = finished || event.kind == "finish"
		case "chunk":
			if event.part == PartBody {
				captured = append(captured, event.raw...)
			}
		}
	}
	require.True(t, bound)
	require.True(t, finished)
	require.Equal(t, raw, captured)
}
