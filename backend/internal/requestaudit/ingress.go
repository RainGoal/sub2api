package requestaudit

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ingressContextKey struct{}

// Ingress keeps the client-facing identity independent of capture availability.
// In particular, manual session bans continue to work when capture is disabled.
type Ingress struct {
	manager       *Manager
	capture       *Capture
	meta          RequestMeta
	mu            sync.Mutex
	id            Identity
	seq           int64
	snapshotBytes int64
	snapshotReady bool
	finished      bool
}

func BeginIngress(manager *Manager, meta RequestMeta) *Ingress {
	if manager == nil {
		return nil
	}
	if meta.ID == "" {
		meta.ID = uuid.NewString()
	}
	i := &Ingress{manager: manager, capture: manager.Begin(meta), meta: RequestMeta{ID: meta.ID}}
	if i.capture != nil && (meta.Kind == "websocket_handshake" || strings.EqualFold(meta.Headers.Get("Upgrade"), "websocket")) {
		size := snapshotSize(meta)
		if manager.reserve(size, true) {
			meta.Headers = meta.Headers.Clone()
			meta.TransferEncoding = append([]string(nil), meta.TransferEncoding...)
			i.meta, i.snapshotBytes, i.snapshotReady = meta, size, true
		} else {
			manager.droppedRequests.Add(1)
			manager.warn("WebSocket handshake snapshot budget exhausted; message capture unavailable for this connection")
		}
	}
	return i
}

func snapshotSize(meta RequestMeta) int64 {
	size := eventOverhead + int64(len(meta.Method)+len(meta.RoutePattern)+len(meta.Protocol)+len(meta.ClientRequestID)+len(meta.ConnectionID)+len(meta.MessageType)+len(meta.HeaderSource)+len(meta.Host)+len(meta.RequestURI))
	for name, values := range meta.Headers {
		size += 64 + int64(len(name))
		for _, value := range values {
			size += 32 + int64(len(value))
		}
	}
	for _, value := range meta.TransferEncoding {
		size += 32 + int64(len(value))
	}
	return size
}

func WithIngress(ctx context.Context, ingress *Ingress) context.Context {
	if ctx == nil || ingress == nil {
		return ctx
	}
	return context.WithValue(ctx, ingressContextKey{}, ingress)
}

func IngressFromContext(ctx context.Context) *Ingress {
	if ctx == nil {
		return nil
	}
	ingress, _ := ctx.Value(ingressContextKey{}).(*Ingress)
	return ingress
}

func (i *Ingress) Capture() *Capture {
	if i == nil {
		return nil
	}
	return i.capture
}

// DeviceTrackingEnabled avoids parsing unused device metadata when both capture
// and device policy are disabled. Existing bound identities remain unchanged.
func (i *Ingress) DeviceTrackingEnabled() bool {
	return i != nil && (i.capture != nil || i.manager != nil && i.manager.DeviceBanEnabled())
}

func (i *Ingress) Identity() Identity {
	if i == nil {
		return Identity{}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.id
}

// SetModel retains the original client model before provider-specific rewrites.
func (i *Ingress) SetModel(model string) {
	if i == nil || model == "" || !validSummaryText(model) || len(model) > 1024 {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.finished && i.id.Model == "" {
		i.id.Model = model
		i.capture.Bind(i.id)
	}
}

// BindIdentity updates the authenticated identity while retaining the original
// client session. Fallback routing must not replace the ingress API key here.
func (i *Ingress) BindIdentity(userID, apiKeyID, groupID int64) {
	if i == nil || apiKeyID <= 0 {
		return
	}
	i.mu.Lock()
	if i.id.APIKeyID == 0 {
		i.id.UserID, i.id.APIKeyID, i.id.GroupID = userID, apiKeyID, groupID
	}
	id := i.id
	i.capture.Bind(id)
	i.mu.Unlock()
}

// SetSession accepts a stable client-provided identifier only while no earlier
// source has resolved the session; headers therefore take priority over body.
func (i *Ingress) SetSession(sessionID, source, resolution string) bool {
	if i == nil || sessionID == "" && resolution == "" {
		return false
	}
	i.mu.Lock()
	if i.id.SessionID != "" {
		i.mu.Unlock()
		return false
	}
	i.id.SessionID = sessionID
	i.id.SessionSource = source
	i.id.SessionResolution = resolution
	id := i.id
	i.capture.Bind(id)
	i.mu.Unlock()
	return true
}

func (i *Ingress) CheckBan(ctx context.Context) (*SessionBan, error) {
	if i == nil || i.manager == nil || !i.manager.SessionBanEnabled() {
		return nil, nil
	}
	id := i.Identity()
	if id.APIKeyID <= 0 || id.SessionID == "" {
		return nil, nil
	}
	return i.manager.CheckBan(ctx, id.APIKeyID, id.SessionID)
}

// SetDevice preserves the first valid inbound identity, before upstream rewrites.
// A missing or invalid header can still be completed from the original body.
func (i *Ingress) SetDevice(deviceType, deviceID, source, resolution string) bool {
	if i == nil || deviceID == "" && resolution == "" {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.finished || ValidDeviceIdentity(i.id.DeviceType, i.id.DeviceID) {
		return false
	}
	if !ValidDeviceIdentity(deviceType, deviceID) {
		deviceID = ""
		if deviceType != DeviceTypeClaude && deviceType != DeviceTypeCodex {
			deviceType = ""
		}
	}
	i.id.DeviceType, i.id.DeviceID = deviceType, deviceID
	i.id.DeviceSource, i.id.DeviceResolution = safeSummaryText(source), safeSummaryText(resolution)
	i.capture.Bind(i.id)
	return true
}

func (i *Ingress) CheckDeviceBan(ctx context.Context) (*DeviceBan, error) {
	if i == nil || i.manager == nil || !i.manager.DeviceBanEnabled() {
		return nil, nil
	}
	id := i.Identity()
	if id.APIKeyID <= 0 {
		return nil, nil
	}
	return i.manager.CheckDeviceBan(ctx, id.DeviceType, id.DeviceID)
}

// RecordMessage captures one client-to-server WS frame without retaining a
// second payload copy. It is safe to call from a connection reader goroutine.
func (i *Ingress) RecordMessage(messageType string, payload []byte) {
	if i == nil || i.manager == nil || !i.manager.Enabled() {
		return
	}
	i.mu.Lock()
	if i.finished || !i.snapshotReady {
		i.mu.Unlock()
		return
	}
	i.seq++
	seq := i.seq
	id := i.id
	meta := i.meta
	meta.ID = ""
	meta.Kind = "websocket_message"
	meta.Protocol = "websocket"
	meta.ReceivedAt = time.Now()
	meta.ConnectionID = i.meta.ID
	meta.HeaderSource = "handshake"
	meta.MessageSeq = seq
	meta.MessageType = messageType
	meta.ContentLength = int64(len(payload))
	capture := i.manager.Begin(meta)
	i.mu.Unlock()
	if capture == nil {
		return
	}
	capture.Bind(id)
	capture.Append("body", payload)
	capture.EndPart("body", true, "")
	capture.Complete(Completion{Outcome: "received"})
	capture.Finish()
}

func (i *Ingress) Finish(status int, started time.Time, outcome string, trailers http.Header) {
	if i == nil {
		return
	}
	i.mu.Lock()
	if i.finished {
		i.mu.Unlock()
		return
	}
	i.finished = true
	if i.snapshotBytes > 0 {
		i.manager.queueBytes.Add(-i.snapshotBytes)
		i.snapshotBytes = 0
	}
	i.meta = RequestMeta{}
	i.mu.Unlock()
	if i.capture == nil {
		return
	}
	i.capture.Trailers(trailers)
	i.capture.Complete(Completion{HTTPStatus: status, DurationMS: time.Since(started).Milliseconds(), Outcome: outcome})
	i.capture.Finish()
}
