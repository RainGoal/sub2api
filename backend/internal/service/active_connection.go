package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	ActiveConnectionPhaseQueued     = "queued"
	ActiveConnectionPhaseConnecting = "connecting"
	ActiveConnectionPhaseReceiving  = "receiving"
	ActiveConnectionPhaseFinalizing = "finalizing"
	ActiveConnectionStatusCompleted = "completed"
	ActiveConnectionStatusFailed    = "failed"
	// Keep enough room for a burst of hundreds of starts without making a slow
	// monitoring panel block or immediately lose the initial event sequence.
	activeConnectionSubscriberBuffer = 512
	activeConnectionTTL              = 10 * time.Minute
)

// ActiveConnection is an intentionally small, user-safe view of an in-flight
// request. It is runtime state only and must never be used for billing.
type ActiveConnection struct {
	RequestID    string    `json:"request_id"`
	Model        string    `json:"model"`
	RequestType  string    `json:"request_type"`
	Stream       bool      `json:"stream"`
	APIKeyName   string    `json:"api_key_name,omitempty"`
	Phase        string    `json:"phase"`
	StartedAt    time.Time `json:"started_at"`
	ElapsedMs    int64     `json:"elapsed_ms"`
	FirstTokenMs *int64    `json:"first_token_ms,omitempty"`
}

type ActiveConnectionStart struct {
	RequestID   string
	Model       string
	RequestType string
	Stream      bool
	APIKeyName  string
}

type ActiveConnectionEvent struct {
	Type       string            `json:"-"`
	Connection *ActiveConnection `json:"connection,omitempty"`
	RequestID  string            `json:"request_id,omitempty"`
	Message    string            `json:"message,omitempty"`
}

type activeConnectionEntry struct {
	connection ActiveConnection
	startedAt  time.Time
}

type activeConnectionSubscriber struct {
	ch chan ActiveConnectionEvent
}

// ActiveConnectionService owns best-effort in-memory state for the user panel.
// All operations are non-blocking with respect to gateway requests: subscriber
// delivery is buffered and drops updates when a client is too slow.
type ActiveConnectionService struct {
	mu          sync.RWMutex
	byUser      map[int64]map[string]*activeConnectionEntry
	subscribers map[int64]map[uint64]*activeConnectionSubscriber
	nextSubID   atomic.Uint64
	now         func() time.Time
}

func NewActiveConnectionService() *ActiveConnectionService {
	return &ActiveConnectionService{
		byUser:      make(map[int64]map[string]*activeConnectionEntry),
		subscribers: make(map[int64]map[uint64]*activeConnectionSubscriber),
		now:         time.Now,
	}
}

// ActiveConnectionHandle is stored in request context by the gateway
// middleware. Methods are deliberately idempotent so error and panic paths can
// safely call Finish more than once.
type ActiveConnectionHandle struct {
	service   *ActiveConnectionService
	userID    int64
	requestID string
	finishMu  sync.Mutex
	finished  bool
}

func (s *ActiveConnectionService) Start(userID int64, input ActiveConnectionStart) *ActiveConnectionHandle {
	if s == nil || userID <= 0 {
		return nil
	}
	requestID := strings.TrimSpace(input.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	now := s.currentTime()
	connection := ActiveConnection{
		RequestID:   requestID,
		Model:       truncateActiveValue(input.Model, 160),
		RequestType: truncateActiveValue(input.RequestType, 80),
		Stream:      input.Stream,
		APIKeyName:  truncateActiveValue(input.APIKeyName, 120),
		Phase:       ActiveConnectionPhaseQueued,
		StartedAt:   now.UTC(),
	}

	s.mu.Lock()
	s.pruneLocked(now)
	userConnections := s.byUser[userID]
	if userConnections == nil {
		userConnections = make(map[string]*activeConnectionEntry)
		s.byUser[userID] = userConnections
	}
	if _, exists := userConnections[requestID]; exists {
		s.mu.Unlock()
		return nil
	}
	userConnections[requestID] = &activeConnectionEntry{connection: connection, startedAt: now}
	s.publishLocked(userID, ActiveConnectionEvent{Type: "connection.started", Connection: cloneActiveConnection(&connection)})
	s.mu.Unlock()

	return &ActiveConnectionHandle{service: s, userID: userID, requestID: requestID}
}

func (s *ActiveConnectionService) Snapshot(userID int64) []ActiveConnection {
	if s == nil || userID <= 0 {
		return nil
	}
	now := s.currentTime()
	s.mu.Lock()
	s.pruneLocked(now)
	items := s.snapshotLocked(userID, now)
	s.mu.Unlock()
	return items
}

// Subscribe installs the subscriber before taking the snapshot, so events
// arriving during initial page load cannot be lost.
func (s *ActiveConnectionService) Subscribe(userID int64) ([]ActiveConnection, <-chan ActiveConnectionEvent, func()) {
	if s == nil || userID <= 0 {
		return nil, nil, func() {}
	}
	now := s.currentTime()
	subID := s.nextSubID.Add(1)
	subscriber := &activeConnectionSubscriber{ch: make(chan ActiveConnectionEvent, activeConnectionSubscriberBuffer)}
	s.mu.Lock()
	s.pruneLocked(now)
	if s.subscribers[userID] == nil {
		s.subscribers[userID] = make(map[uint64]*activeConnectionSubscriber)
	}
	s.subscribers[userID][subID] = subscriber
	snapshot := s.snapshotLocked(userID, now)
	s.mu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			s.mu.Lock()
			if users := s.subscribers[userID]; users != nil {
				delete(users, subID)
				if len(users) == 0 {
					delete(s.subscribers, userID)
				}
			}
			s.mu.Unlock()
		})
	}
	return snapshot, subscriber.ch, cleanup
}

func (h *ActiveConnectionHandle) UpdateMetadata(model string, stream bool, requestType string) {
	if h == nil || h.service == nil || h.isFinished() {
		return
	}
	s := h.service
	s.mu.Lock()
	entry := s.entryLocked(h.userID, h.requestID)
	if entry == nil {
		s.mu.Unlock()
		return
	}
	changed := false
	if strings.TrimSpace(model) != "" && entry.connection.Model != strings.TrimSpace(model) {
		entry.connection.Model = truncateActiveValue(model, 160)
		changed = true
	}
	if entry.connection.Stream != stream {
		entry.connection.Stream = stream
		changed = true
	}
	if strings.TrimSpace(requestType) != "" && entry.connection.RequestType != strings.TrimSpace(requestType) {
		entry.connection.RequestType = truncateActiveValue(requestType, 80)
		changed = true
	}
	if entry.connection.Phase == ActiveConnectionPhaseQueued {
		entry.connection.Phase = ActiveConnectionPhaseConnecting
		changed = true
	}
	if changed {
		entry.connection.ElapsedMs = elapsedMilliseconds(entry.startedAt, s.currentTime())
		s.publishLocked(h.userID, ActiveConnectionEvent{Type: "connection.updated", Connection: cloneActiveConnection(&entry.connection)})
	}
	s.mu.Unlock()
}

func (h *ActiveConnectionHandle) UpdatePhase(phase string) {
	if h == nil || h.service == nil || h.isFinished() {
		return
	}
	phase = normalizeActivePhase(phase)
	if phase == "" {
		return
	}
	s := h.service
	s.mu.Lock()
	entry := s.entryLocked(h.userID, h.requestID)
	if entry != nil && entry.connection.Phase != phase {
		entry.connection.Phase = phase
		entry.connection.ElapsedMs = elapsedMilliseconds(entry.startedAt, s.currentTime())
		s.publishLocked(h.userID, ActiveConnectionEvent{Type: "connection.updated", Connection: cloneActiveConnection(&entry.connection)})
	}
	s.mu.Unlock()
}

func (h *ActiveConnectionHandle) MarkFirstData(payload string) {
	if h == nil || h.service == nil || h.isFinished() || strings.TrimSpace(payload) == "" || strings.TrimSpace(payload) == "[DONE]" {
		return
	}
	s := h.service
	now := s.currentTime()
	s.mu.Lock()
	entry := s.entryLocked(h.userID, h.requestID)
	if entry != nil && entry.connection.FirstTokenMs == nil {
		ms := elapsedMilliseconds(entry.startedAt, now)
		entry.connection.FirstTokenMs = &ms
		entry.connection.Phase = ActiveConnectionPhaseReceiving
		entry.connection.ElapsedMs = ms
		s.publishLocked(h.userID, ActiveConnectionEvent{Type: "connection.updated", Connection: cloneActiveConnection(&entry.connection)})
	}
	s.mu.Unlock()
}

func (h *ActiveConnectionHandle) Finish(status, message string) {
	if h == nil || h.service == nil {
		return
	}
	h.finishMu.Lock()
	if h.finished {
		h.finishMu.Unlock()
		return
	}
	h.finished = true
	h.finishMu.Unlock()

	s := h.service
	now := s.currentTime()
	s.mu.Lock()
	if users := s.byUser[h.userID]; users != nil {
		if _, exists := users[h.requestID]; exists {
			delete(users, h.requestID)
			if len(users) == 0 {
				delete(s.byUser, h.userID)
			}
			status = normalizeActiveStatus(status)
			event := ActiveConnectionEvent{Type: "connection." + status, RequestID: h.requestID, Message: truncateActiveValue(message, 200)}
			s.publishLocked(h.userID, event)
		}
	}
	_ = now
	s.mu.Unlock()
}

func (s *ActiveConnectionService) PruneExpired() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.pruneLocked(s.currentTime())
	s.mu.Unlock()
}

func (s *ActiveConnectionService) currentTime() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *ActiveConnectionService) entryLocked(userID int64, requestID string) *activeConnectionEntry {
	if users := s.byUser[userID]; users != nil {
		return users[requestID]
	}
	return nil
}

func (s *ActiveConnectionService) snapshotLocked(userID int64, now time.Time) []ActiveConnection {
	users := s.byUser[userID]
	items := make([]ActiveConnection, 0, len(users))
	for _, entry := range users {
		connection := *cloneActiveConnection(&entry.connection)
		connection.ElapsedMs = elapsedMilliseconds(entry.startedAt, now)
		items = append(items, connection)
	}
	return items
}

func (s *ActiveConnectionService) pruneLocked(now time.Time) {
	for userID, users := range s.byUser {
		for requestID, entry := range users {
			if now.Sub(entry.startedAt) <= activeConnectionTTL {
				continue
			}
			delete(users, requestID)
			s.publishLocked(userID, ActiveConnectionEvent{Type: "connection.failed", RequestID: requestID, Message: "request timed out"})
		}
		if len(users) == 0 {
			delete(s.byUser, userID)
		}
	}
}

func (s *ActiveConnectionService) publishLocked(userID int64, event ActiveConnectionEvent) {
	for _, subscriber := range s.subscribers[userID] {
		select {
		case subscriber.ch <- event:
		default:
			// A slow panel must never block an AI request. Terminal events get
			// one best-effort replacement slot so stale rows clear promptly.
			if event.Type == "connection.completed" || event.Type == "connection.failed" {
				select {
				case <-subscriber.ch:
				default:
				}
				select {
				case subscriber.ch <- event:
				default:
				}
			}
		}
	}
}

func (h *ActiveConnectionHandle) isFinished() bool {
	h.finishMu.Lock()
	finished := h.finished
	h.finishMu.Unlock()
	return finished
}

func cloneActiveConnection(value *ActiveConnection) *ActiveConnection {
	if value == nil {
		return nil
	}
	clone := *value
	if value.FirstTokenMs != nil {
		first := *value.FirstTokenMs
		clone.FirstTokenMs = &first
	}
	return &clone
}

func elapsedMilliseconds(start, now time.Time) int64 {
	if now.Before(start) {
		return 0
	}
	return now.Sub(start).Milliseconds()
}

func normalizeActivePhase(phase string) string {
	switch strings.TrimSpace(phase) {
	case ActiveConnectionPhaseQueued, ActiveConnectionPhaseConnecting, ActiveConnectionPhaseReceiving, ActiveConnectionPhaseFinalizing:
		return strings.TrimSpace(phase)
	default:
		return ""
	}
}

func normalizeActiveStatus(status string) string {
	if strings.TrimSpace(status) == ActiveConnectionStatusFailed {
		return ActiveConnectionStatusFailed
	}
	return ActiveConnectionStatusCompleted
}

func truncateActiveValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

type activeConnectionContextKey struct{}

func WithActiveConnection(ctx context.Context, handle *ActiveConnectionHandle) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if handle == nil {
		return ctx
	}
	return context.WithValue(ctx, activeConnectionContextKey{}, handle)
}

func activeConnectionFromContext(ctx context.Context) *ActiveConnectionHandle {
	if ctx == nil {
		return nil
	}
	handle, _ := ctx.Value(activeConnectionContextKey{}).(*ActiveConnectionHandle)
	return handle
}

func UpdateActiveConnectionMetadata(ctx context.Context, model string, stream bool, requestType string) {
	if handle := activeConnectionFromContext(ctx); handle != nil {
		handle.UpdateMetadata(model, stream, requestType)
	}
}

func UpdateActiveConnectionPhase(ctx context.Context, phase string) {
	if handle := activeConnectionFromContext(ctx); handle != nil {
		handle.UpdatePhase(phase)
	}
}

func MarkFirstSSEData(ctx context.Context, payload string) {
	if handle := activeConnectionFromContext(ctx); handle != nil {
		handle.MarkFirstData(payload)
	}
}
