package requestaudit

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type Capture struct {
	manager         *Manager
	id              string
	mu              sync.Mutex
	seq             map[string]int
	bytes           map[string]int64
	stored          map[string]int64
	manifests       map[string]PartManifest
	identity        Identity
	completion      *Completion
	finished        bool
	dropped         bool
	policy          *GroupPolicy
	remaining       atomic.Int64
	attempts        atomic.Int32
	contention      atomic.Bool
	truncated       map[string]bool
	deferredHeaders bool
}

func (m *Manager) Begin(meta RequestMeta) *Capture {
	return m.BeginWithPolicy(meta, nil)
}

func (m *Manager) BeginWithPolicy(meta RequestMeta, policy *GroupPolicy) *Capture {
	if m == nil || !m.Enabled() {
		return nil
	}
	// Reserve before building temporary maps or JSON. Eight times the snapshot
	// estimate covers worst-case JSON escaping and the intermediate envelope.
	if meta.ID == "" {
		meta.ID = uuid.NewString()
	}
	if meta.ReceivedAt.IsZero() {
		meta.ReceivedAt = time.Now().UTC()
	}
	parent := meta
	parent.Headers = nil
	parent.TransferEncoding = nil
	c := &Capture{manager: m, id: meta.ID, seq: make(map[string]int), bytes: make(map[string]int64), stored: make(map[string]int64), manifests: make(map[string]PartManifest), policy: policy}
	if policy != nil {
		c.remaining.Store(256 * 1024)
		c.truncated = make(map[string]bool)
		if !policy.InboundBody {
			c.manifests[PartBody] = PartManifest{Complete: true, Reason: "not_selected"}
		}
		c.manifests[PartTrailers] = PartManifest{Complete: true, Reason: "not_selected"}
	}
	inputSize := int64(0)
	if policy == nil || policy.InboundHeaders {
		inputSize = snapshotSize(meta)
	}
	if policy == nil && !m.reserve(inputSize*8, true) {
		m.droppedRequests.Add(1)
		return nil
	}
	if policy == nil {
		defer m.queueBytes.Add(-inputSize * 8)
	}
	if !m.enqueue(auditEvent{kind: "begin", id: c.id, meta: &parent}) {
		m.droppedRequests.Add(1)
		return nil
	}
	m.accepted.Add(1)
	if inputSize == 0 {
		c.EndPart(PartHeaders, true, "not_selected")
		return c
	}
	if policy != nil {
		if inputSize > 16*1024 {
			c.EndPart(PartHeaders, false, "size_limit")
			return c
		}
		if !m.reserve(inputSize*8, true) {
			c.EndPart(PartHeaders, false, "queue_full")
			return c
		}
		headerMeta := meta
		headerMeta.Headers = meta.Headers.Clone()
		headerMeta.TransferEncoding = append([]string(nil), meta.TransferEncoding...)
		headerEvent := auditEvent{
			kind: "header_snapshot", id: c.id, part: PartHeaders, seq: 0,
			headerMeta: &headerMeta, queued: inputSize * 8, payloadEvent: true,
		}
		if !m.enqueueReserved(headerEvent) {
			c.EndPart(PartHeaders, false, "queue_full")
			return c
		}
		c.seq[PartHeaders] = 1
		c.stored[PartHeaders] = -1
		c.deferredHeaders = true
		c.EndPart(PartHeaders, true, "")
		return c
	}
	// Include request-line fields and every original header value in the payload.
	headerRaw, err := marshalRequestAuditHeaders(meta)
	if err != nil {
		c.EndPart(PartHeaders, false, "encoding_error")
		return c
	}
	if policy != nil && len(headerRaw) > 16*1024 {
		c.EndPart(PartHeaders, false, "size_limit")
		return c
	}
	c.Append(PartHeaders, headerRaw)
	c.EndPart(PartHeaders, true, "")
	return c
}

func (c *Capture) Bind(id Identity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	c.identity = id
	c.manager.enqueue(auditEvent{kind: "bind", id: c.id, identity: &id})
}

func (c *Capture) Append(part string, p []byte) {
	if c == nil || len(p) == 0 || !validPart(part) {
		return
	}
	if !c.mu.TryLock() {
		c.contention.Store(true)
		c.manager.droppedEvents.Add(1)
		return
	}
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	if _, ended := c.manifests[part]; ended {
		return
	}
	if c.policy != nil {
		c.bytes[part] += int64(len(p))
		if c.truncated[part] || c.dropped || c.contention.Load() {
			return
		}
		n := c.claim(min(len(p), max(0, 64*1024-int(c.stored[part]))))
		c.truncated[part] = n < len(p)
		if n != 0 {
			if c.manager.enqueueChunk(auditEvent{kind: "chunk", id: c.id, part: part, seq: c.seq[part]}, p[:n]) {
				c.seq[part]++
				c.stored[part] += int64(n)
			} else {
				c.dropped = true
			}
		}
		return
	}
	for len(p) > 0 {
		n := len(p)
		if n > ChunkBytes {
			n = ChunkBytes
		}
		seq := c.seq[part]
		c.seq[part]++
		c.bytes[part] += int64(n)
		if c.dropped || c.contention.Load() {
			// Preserve a continuous prefix after pressure first exhausts the budget.
			p = p[n:]
			continue
		}
		if c.manager.enqueueChunk(auditEvent{kind: "chunk", id: c.id, part: part, seq: seq}, p[:n]) {
			c.stored[part] += int64(n)
		} else {
			c.dropped = true
		}
		p = p[n:]
	}
}

func (c *Capture) claim(n int) int {
	for range 4 {
		remaining := c.remaining.Load()
		take := min(int64(n), remaining)
		if c.remaining.CompareAndSwap(remaining, remaining-take) {
			return int(take)
		}
	}
	return 0
}

func (c *Capture) EndPart(part string, complete bool, reason string) {
	if c == nil || !validPart(part) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	if _, ended := c.manifests[part]; ended {
		return
	}
	if c.dropped || c.contention.Load() {
		complete = false
		if reason == "" {
			reason = "queue_full"
		}
	}
	if c.truncated[part] && reason == "" {
		complete, reason = false, "size_limit"
	}
	manifest := PartManifest{ExpectedChunks: c.seq[part], ExpectedBytes: c.bytes[part], Complete: complete, Reason: reason}
	if c.policy != nil {
		manifest.ExpectedBytes = c.stored[part]
	}
	if c.deferredHeaders && part == PartHeaders {
		manifest.ExpectedBytes = -1
		manifest.Reason = "deferred_encoding"
	}
	c.manifests[part] = manifest
	c.manager.enqueue(auditEvent{kind: "part_end", id: c.id, part: part, manifest: &manifest})
}

func marshalRequestAuditHeaders(meta RequestMeta) ([]byte, error) {
	return json.Marshal(map[string]any{
		"headers":           envelopeHeaders(meta.Headers),
		"host":              envelopeText(meta.Host),
		"request_uri":       envelopeText(meta.RequestURI),
		"method":            meta.Method,
		"protocol":          meta.Protocol,
		"transfer_encoding": meta.TransferEncoding,
		"content_length":    meta.ContentLength,
		"header_source":     meta.HeaderSource,
	})
}

func (c *Capture) Complete(done Completion) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.completion = &done
	c.mu.Unlock()
}

func (c *Capture) Finish() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	if _, ok := c.manifests[PartBody]; !ok {
		c.manifests[PartBody] = PartManifest{ExpectedChunks: c.seq[PartBody], ExpectedBytes: c.bytes[PartBody], Reason: "body_not_fully_read"}
	}
	if _, ok := c.manifests[PartTrailers]; !ok {
		c.manifests[PartTrailers] = PartManifest{Complete: true}
	}
	manifests := make(map[string]PartManifest, len(c.manifests))
	for k, v := range c.manifests {
		manifests[k] = v
	}
	var completion Completion
	if c.completion != nil {
		completion = *c.completion
	}
	id := c.identity
	observed, stored := c.bytes[PartBody], c.stored[PartBody]
	dropped := c.dropped || c.contention.Load()
	c.mu.Unlock()
	if !c.manager.enqueue(auditEvent{kind: "finish", id: c.id, identity: &id, completion: &completion, manifests: manifests, observedBodyBytes: observed, storedBodyBytes: stored, captureState: stateForManifests(manifests, dropped)}) {
		c.manager.droppedRequests.Add(1)
	}
}

func stateForManifests(manifests map[string]PartManifest, dropped bool) string {
	if dropped {
		return "partial"
	}
	for _, v := range manifests {
		if !v.Complete {
			return "partial"
		}
	}
	return "complete"
}

func (c *Capture) Trailers(headers http.Header) {
	if c == nil {
		return
	}
	c.mu.Lock()
	_, ended := c.manifests[PartTrailers]
	finished := c.finished
	c.mu.Unlock()
	if ended || finished {
		return
	}
	workBytes := snapshotSize(RequestMeta{Headers: headers}) * 8
	if !c.manager.reserve(workBytes, true) {
		c.manager.droppedEvents.Add(1)
		c.EndPart(PartTrailers, false, "queue_full")
		return
	}
	defer c.manager.queueBytes.Add(-workBytes)
	raw, err := json.Marshal(map[string]any{"headers": envelopeHeaders(headers)})
	if err != nil {
		c.EndPart(PartTrailers, false, "encoding_error")
		return
	}
	c.Append(PartTrailers, raw)
	c.EndPart(PartTrailers, true, "")
}

func (c *Capture) WrapBody(body io.ReadCloser) io.ReadCloser {
	return c.WrapBodyWithTrailers(body, nil)
}

// WrapBodyWithTrailers finalizes bytes at EOF, independently of a long response.
func (c *Capture) WrapBodyWithTrailers(body io.ReadCloser, trailers func() http.Header) io.ReadCloser {
	if c == nil || body == nil {
		return body
	}
	if body == http.NoBody {
		c.EndPart(PartBody, true, "")
		if trailers != nil {
			c.Trailers(trailers())
		}
		return body
	}
	return &captureBody{ReadCloser: body, c: c, trailers: trailers}
}

type captureBody struct {
	io.ReadCloser
	c        *Capture
	ended    bool
	trailers func() http.Header
}

func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.c.Append(PartBody, p[:n])
	}
	if err != nil && !b.ended {
		b.ended = true
		reason := ""
		if !errors.Is(err, io.EOF) {
			reason = "read_error"
		}
		b.c.EndPart(PartBody, errors.Is(err, io.EOF), reason)
		if errors.Is(err, io.EOF) && b.trailers != nil {
			b.c.Trailers(b.trailers())
		}
	}
	return n, err
}
func (b *captureBody) Close() error {
	if !b.ended {
		b.ended = true
		b.c.EndPart(PartBody, false, "closed_before_eof")
	}
	return b.ReadCloser.Close()
}
