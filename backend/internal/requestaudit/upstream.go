package requestaudit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	UpstreamRequestHeaders  = "request_headers"
	UpstreamRequestBody     = "request_body"
	UpstreamResponseHeaders = "response_headers"
	UpstreamResponseBody    = "response_body"
)

var upstreamParts = [...]string{UpstreamRequestHeaders, UpstreamRequestBody, UpstreamResponseHeaders, UpstreamResponseBody}

func validUpstreamPart(part string) bool {
	for _, candidate := range upstreamParts {
		if part == candidate {
			return true
		}
	}
	return false
}

func isBodyPart(part string) bool {
	return part == UpstreamRequestBody || part == UpstreamResponseBody
}

type AttemptRecord struct {
	AttemptNo  int                     `json:"attempt_no"`
	ReceivedAt time.Time               `json:"received_at"`
	Method     string                  `json:"method"`
	Host       string                  `json:"host"`
	Path       string                  `json:"path"`
	AccountID  int64                   `json:"account_id"`
	StatusCode int                     `json:"status_code"`
	Parts      map[string]PartManifest `json:"parts"`
}

type upstreamPartState struct {
	chunks int
	bytes  int
	reason string
	ended  atomic.Bool
}

type headerField struct {
	Name   string `json:"name"`
	Values []any  `json:"values"`
}

type UpstreamAttempt struct {
	capture      *Capture
	number       int
	parts        map[string]*upstreamPartState
	mu           sync.Mutex
	headerFields []headerField
	headerBytes  int64
}

func BeginUpstreamAttempt(ctx context.Context, method, host, path string, accountID int64) *UpstreamAttempt {
	ingress := IngressFromContext(ctx)
	if ingress == nil {
		return nil
	}
	c := ingress.Capture()
	if c == nil || c.policy == nil || c.manager.upstreamCrypto == nil || !c.manager.Enabled() {
		return nil
	}
	selection := [...]bool{c.policy.UpstreamRequestHeaders, c.policy.UpstreamRequestBody,
		c.policy.UpstreamResponseHeaders, c.policy.UpstreamResponseBody}
	if !selection[0] && !selection[1] && !selection[2] && !selection[3] {
		return nil
	}
	used := c.attempts.Load()
	if used >= 3 || !c.attempts.CompareAndSwap(used, used+1) {
		return nil
	}
	number := int(used + 1)
	attempt := &UpstreamAttempt{capture: c, number: number, parts: make(map[string]*upstreamPartState, len(selection))}
	record := &AttemptRecord{AttemptNo: number, ReceivedAt: time.Now().UTC(), Method: method, Host: host,
		Path: path, AccountID: accountID, Parts: make(map[string]PartManifest, len(selection))}
	for i, part := range upstreamParts {
		if selection[i] {
			attempt.parts[part] = &upstreamPartState{}
			record.Parts[part] = PartManifest{Reason: "capturing"}
		} else {
			record.Parts[part] = PartManifest{Complete: true, Reason: "not_selected"}
		}
	}
	if !c.manager.enqueue(auditEvent{kind: "attempt_begin", id: c.id, attemptNo: number, attempt: record}) {
		return nil
	}
	return attempt
}

func (a *UpstreamAttempt) Enabled(part string) bool {
	return a != nil && a.parts[part] != nil
}

func (a *UpstreamAttempt) SetStatus(status int) {
	if a != nil {
		a.capture.manager.enqueue(auditEvent{kind: "attempt_status", id: a.capture.id, attemptNo: a.number,
			attempt: &AttemptRecord{StatusCode: status}})
	}
}

func (a *UpstreamAttempt) append(part string, data []byte) {
	if !a.Enabled(part) || len(data) == 0 {
		return
	}
	if !a.mu.TryLock() {
		a.failPart(part, "queue_full")
		return
	}
	defer a.mu.Unlock()
	a.appendLocked(part, data)
}

func (a *UpstreamAttempt) appendLocked(part string, data []byte) {
	state := a.parts[part]
	if state.ended.Load() || state.reason != "" {
		return
	}
	c := a.capture
	n := c.claim(min(len(data), 64*1024-state.bytes))
	if n < len(data) {
		state.reason = "size_limit"
	}
	if n > 0 && !isBodyPart(part) && n < len(data) {
		return
	}
	if n > 0 {
		if c.manager.enqueueChunk(auditEvent{kind: "attempt_chunk", id: c.id, attemptNo: a.number, part: part, seq: state.chunks}, data[:n]) {
			state.bytes += n
			state.chunks++
		} else {
			state.reason = "queue_full"
		}
	}
}

func (a *UpstreamAttempt) EndPart(part string, complete bool, reason string) {
	if !a.Enabled(part) {
		return
	}
	if !a.mu.TryLock() {
		a.failPart(part, "queue_full")
		return
	}
	defer a.mu.Unlock()
	a.endLocked(part, complete, reason)
}

func (a *UpstreamAttempt) endLocked(part string, complete bool, reason string) {
	state := a.parts[part]
	if !state.ended.CompareAndSwap(false, true) {
		return
	}
	if state.reason != "" {
		complete, reason = false, state.reason
	}
	c := a.capture
	c.manager.enqueue(auditEvent{kind: "attempt_end", id: c.id, attemptNo: a.number, part: part,
		manifest: &PartManifest{ExpectedChunks: state.chunks, ExpectedBytes: int64(state.bytes), Complete: complete, Reason: reason}})
}

func (a *UpstreamAttempt) failPart(part, reason string) {
	state := a.parts[part]
	if state == nil || !state.ended.CompareAndSwap(false, true) {
		return
	}
	a.capture.manager.droppedEvents.Add(1)
	go func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if part == UpstreamRequestHeaders {
			a.releaseRequestHeaderSnapshotLocked()
		}
		a.capture.manager.enqueue(auditEvent{kind: "attempt_end", id: a.capture.id, attemptNo: a.number, part: part,
			manifest: &PartManifest{ExpectedChunks: state.chunks, ExpectedBytes: int64(state.bytes), Complete: false, Reason: reason}})
	}()
}

func (a *UpstreamAttempt) AddRequestHeader(name string, values []string) {
	if !a.Enabled(UpstreamRequestHeaders) {
		return
	}
	if !a.mu.TryLock() {
		a.failPart(UpstreamRequestHeaders, "queue_full")
		return
	}
	defer a.mu.Unlock()
	state := a.parts[UpstreamRequestHeaders]
	if state.ended.Load() || state.reason != "" {
		return
	}
	size := headerFieldSize(name, values)
	if a.headerBytes+size > 16*1024 {
		state.reason = "size_limit"
		return
	}
	if !a.capture.manager.reserve(size*8, true) {
		state.reason = "queue_full"
		return
	}
	a.headerBytes += size
	a.headerFields = append(a.headerFields, makeHeaderField(name, values))
}

func (a *UpstreamAttempt) EndRequestHeaders() {
	a.finishRequestHeaders(true, "")
}

func (a *UpstreamAttempt) AbortRequestHeaders() {
	a.finishRequestHeaders(false, "transport_error")
}

func (a *UpstreamAttempt) finishRequestHeaders(complete bool, reason string) {
	if !a.Enabled(UpstreamRequestHeaders) {
		return
	}
	if !a.mu.TryLock() {
		a.failPart(UpstreamRequestHeaders, "queue_full")
		return
	}
	defer a.mu.Unlock()
	if a.parts[UpstreamRequestHeaders].ended.Load() {
		a.releaseRequestHeaderSnapshotLocked()
		return
	}
	reserved := a.headerBytes * 8
	a.headerBytes = 0
	fields := a.headerFields
	if a.parts[UpstreamRequestHeaders].reason == "" {
		a.finishHeaderFields(UpstreamRequestHeaders, fields, complete, reason, reserved)
	} else {
		a.capture.manager.queueBytes.Add(-reserved)
		a.endLocked(UpstreamRequestHeaders, false, reason)
	}
	a.headerFields = nil
}

func (a *UpstreamAttempt) releaseRequestHeaderSnapshotLocked() {
	if a.headerBytes > 0 {
		a.capture.manager.queueBytes.Add(-a.headerBytes * 8)
		a.headerBytes = 0
	}
	a.headerFields = nil
}

func headerFieldSize(name string, values []string) int64 {
	size := int64(32 + len(name))
	for _, value := range values {
		size += int64(16 + len(value))
	}
	return size
}

func makeHeaderField(name string, values []string) headerField {
	field := headerField{Name: name, Values: make([]any, len(values))}
	for i, value := range values {
		field.Values[i] = envelopeText(value)
	}
	return field
}

func (a *UpstreamAttempt) CaptureHeaders(part string, headers http.Header) {
	if !a.Enabled(part) {
		return
	}
	if !a.mu.TryLock() {
		a.failPart(part, "queue_full")
		return
	}
	defer a.mu.Unlock()
	state := a.parts[part]
	if state.ended.Load() || state.reason != "" {
		return
	}
	size := int64(0)
	for name, values := range headers {
		size += headerFieldSize(name, values)
		if size > 16*1024 {
			a.endLocked(part, false, "size_limit")
			return
		}
	}
	if !a.capture.manager.reserve(size*8, true) {
		a.endLocked(part, false, "queue_full")
		return
	}
	fields := make([]headerField, 0, len(headers))
	for name, values := range headers {
		fields = append(fields, makeHeaderField(name, values))
	}
	a.finishHeaderFields(part, fields, true, "", size*8)
}

func (a *UpstreamAttempt) finishHeaderFields(part string, fields []headerField, complete bool, reason string, reserved int64) {
	if !complete && reason != "" && len(fields) == 0 {
		a.capture.manager.queueBytes.Add(-reserved)
		a.endLocked(part, false, reason)
		return
	}
	if !a.capture.manager.reserve(eventOverhead, true) {
		a.capture.manager.queueBytes.Add(-reserved)
		a.endLocked(part, false, "queue_full")
		return
	}
	state := a.parts[part]
	event := auditEvent{
		kind: "attempt_header_snapshot", id: a.capture.id, attemptNo: a.number,
		part: part, seq: state.chunks, headerFields: fields,
		queued: reserved + eventOverhead, payloadEvent: true,
	}
	if !a.capture.manager.enqueueReserved(event) {
		a.endLocked(part, false, "queue_full")
		return
	}
	state.chunks++
	state.bytes = -1
	a.endLocked(part, complete, reason)
}

func (a *UpstreamAttempt) WrapBody(part string, body io.ReadCloser) io.ReadCloser {
	if !a.Enabled(part) {
		return body
	}
	if body == nil || body == http.NoBody {
		a.EndPart(part, true, "")
		return body
	}
	return &upstreamBody{ReadCloser: body, attempt: a, part: part}
}

type upstreamBody struct {
	io.ReadCloser
	attempt *UpstreamAttempt
	part    string
	ended   atomic.Bool
}

func (b *upstreamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && !b.ended.Load() {
		b.attempt.append(b.part, p[:n])
	}
	if err != nil && b.ended.CompareAndSwap(false, true) {
		if errors.Is(err, io.EOF) {
			b.attempt.EndPart(b.part, true, "")
		} else {
			b.attempt.EndPart(b.part, false, "read_error")
		}
	}
	return n, err
}

func (b *upstreamBody) Close() error {
	err := b.ReadCloser.Close()
	if b.ended.CompareAndSwap(false, true) {
		b.attempt.EndPart(b.part, false, "closed_before_eof")
	}
	return err
}
