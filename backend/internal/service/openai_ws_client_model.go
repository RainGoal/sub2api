package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// Display state is separate from routing and billing metadata. A completed
// response keeps its public name until the connection ends, so even a late
// terminal event cannot acquire a subsequent turn's model or usage.
type openAIWSClientModelTurn struct {
	model          string
	sessionVersion uint64
	responseID     string
	startedAt      time.Time
	settled        bool
	observer       upstreamResponseModelObserver
	usage          OpenAIUsage
}

type openAIWSClientModels struct {
	mu                    sync.Mutex
	initialSessionModel   string
	session               *openAIWSClientSessionModel
	pendingSessionUpdates []*openAIWSClientSessionModel
	observedControlModel  string
	active                *openAIWSClientModelTurn
	responses             map[string]string
}

type openAIWSClientSessionModel struct {
	version               uint64
	model                 string
	eventID               string
	previous              *openAIWSClientSessionModel
	rejected              bool
	uncorrelatedErrorSafe bool
}

// Call with the display state's mutex held. Pending versions remain usable for
// providers that omit ACKs; only an attributable rejection removes a candidate.
func (s *openAIWSClientSessionModel) publicModel() string {
	for version := s; version != nil; version = version.previous {
		if !version.rejected && version.model != "" {
			return version.model
		}
	}
	return ""
}

func newOpenAIWSClientModels(model string) *openAIWSClientModels {
	model = strings.Clone(strings.TrimSpace(model))
	m := &openAIWSClientModels{initialSessionModel: model, session: &openAIWSClientSessionModel{model: model}}
	m.beginTurn(model)
	return m
}

func (m *openAIWSClientModels) beginTurn(model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidateUncorrelatedSessionErrors()
	model = strings.TrimSpace(model)
	var sessionVersion uint64
	if model == "" {
		model = m.session.publicModel()
		sessionVersion = m.session.version
	}
	// Bind at submission, before any upstream output. Later ACKs or rejections
	// may change a future implicit request but never this response's name.
	m.active = &openAIWSClientModelTurn{model: strings.Clone(model), sessionVersion: sessionVersion, startedAt: time.Now()}
}

func (m *openAIWSClientModels) requestModelForFrame(payload []byte) string {
	if model := openAIWSPassthroughRequestModelForFrame(payload); model != "" {
		return model
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.session.publicModel()
}

func (m *openAIWSClientModels) queueSessionUpdate(payload []byte) {
	if !json.Valid(payload) || strings.TrimSpace(gjson.GetBytes(payload, "type").String()) != "session.update" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// A later control request makes an uncorrelated error ambiguous for every
	// older candidate, even if that later request is subsequently rejected.
	m.invalidateUncorrelatedSessionErrors()
	m.session = &openAIWSClientSessionModel{
		version: m.session.version + 1, previous: m.session,
		model:                 strings.Clone(openAIWSPassthroughRequestModelFromSessionFrame(payload)),
		eventID:               strings.Clone(gjson.GetBytes(payload, "event_id").Str),
		uncorrelatedErrorSafe: m.active == nil,
	}
	// Model-free updates also consume an ACK, so their confirmations cannot
	// accidentally acknowledge a later model-changing update.
	m.pendingSessionUpdates = append(m.pendingSessionUpdates, m.session)
}

func (m *openAIWSClientModels) noteOtherClientControl() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidateUncorrelatedSessionErrors()
}

func (m *openAIWSClientModels) invalidateUncorrelatedSessionErrors() {
	for _, update := range m.pendingSessionUpdates {
		update.uncorrelatedErrorSafe = false
	}
}

func (m *openAIWSClientModels) consumeSessionUpdate(index int) *openAIWSClientSessionModel {
	update := m.pendingSessionUpdates[index]
	copy(m.pendingSessionUpdates[index:], m.pendingSessionUpdates[index+1:])
	last := len(m.pendingSessionUpdates) - 1
	m.pendingSessionUpdates[last] = nil
	m.pendingSessionUpdates = m.pendingSessionUpdates[:last]
	return update
}

func (m *openAIWSClientModels) rejectedSessionUpdateIndex(payload []byte) int {
	if len(m.pendingSessionUpdates) == 0 || openAIWSClientResponseID(payload) != "" {
		return -1
	}
	if eventID := gjson.GetBytes(payload, "error.event_id").Str; eventID != "" {
		index := -1
		for i, update := range m.pendingSessionUpdates {
			if update.eventID == eventID {
				if index >= 0 { // Reused client IDs are ambiguous.
					return -1
				}
				index = i
			}
		}
		return index
	}
	// An uncorrelated error during a response belongs to that response unless
	// proved otherwise. Multiple pending updates are also ambiguous.
	if m.active != nil || len(m.pendingSessionUpdates) != 1 || !m.pendingSessionUpdates[0].uncorrelatedErrorSafe {
		return -1
	}
	if param := gjson.GetBytes(payload, "error.param").Str; param != "" && param != "session" && !strings.HasPrefix(param, "session.") {
		return -1
	}
	return 0
}

func (m *openAIWSClientModels) observeSessionControl(payload []byte, eventType string) {
	if strings.HasPrefix(eventType, "session.") {
		switch {
		case eventType == "session.created":
			m.observedControlModel = m.initialSessionModel
		case eventType == "session.updated" && len(m.pendingSessionUpdates) > 0:
			// ACK event IDs identify the server event, not the client request.
			// Resolve in submission order without guessing from upstream C.
			m.observedControlModel = m.consumeSessionUpdate(0).publicModel()
		default:
			m.observedControlModel = m.session.publicModel()
		}
		return
	}
	if eventType == "error" {
		if index := m.rejectedSessionUpdateIndex(payload); index >= 0 {
			update := m.consumeSessionUpdate(index)
			m.observedControlModel = update.publicModel()
			update.rejected = true
		}
	}
}

func openAIWSClientResponseID(payload []byte) string {
	values := gjson.GetManyBytes(payload, "response.id", "response_id", "type", "id")
	for _, value := range values[:2] {
		if value.Type == gjson.String && strings.TrimSpace(value.Str) != "" {
			return strings.TrimSpace(value.Str)
		}
	}
	if isOpenAIWSTerminalEvent(values[2].String()) && values[3].Type == gjson.String {
		return strings.TrimSpace(values[3].Str)
	}
	return ""
}

// observe runs before the relay's raw observers and never modifies payload.
// Unrelated or already settled responses may still be delivered to the client,
// but must not settle another turn or overwrite its observed model.
func (m *openAIWSClientModels) observe(msgType coderws.MessageType, payload []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	m.observedControlModel = ""
	if openAIWSClientJSONFrame(msgType, payload) && (eventType == "error" || strings.HasPrefix(eventType, "session.")) && json.Valid(payload) {
		m.observeSessionControl(payload, eventType)
	}
	id := openAIWSClientResponseID(payload)
	if _, completed := m.responses[id]; id != "" && completed {
		return false
	}
	turn := m.active
	if turn == nil {
		return id == ""
	}
	if id != "" {
		if turn.responseID != "" && turn.responseID != id {
			return false
		}
		turn.responseID = strings.Clone(id)
	}
	// Binary usage has always been opaque in the passthrough relay. Preserve
	// that contract while still correlating supported binary JSON for display.
	if msgType == coderws.MessageText {
		turn.observer.ObserveOpenAI(payload, eventType)
		if openAIWSMessageShouldParseUsage(eventType, payload) {
			parseOpenAIWSResponseUsageFromCompletedEvent(payload, &turn.usage)
		}
	}
	return true
}

func (m *openAIWSClientModels) modelForPayload(payload []byte) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	eventType := gjson.GetBytes(payload, "type").String()
	if (eventType == "error" || strings.HasPrefix(eventType, "session.")) && m.observedControlModel != "" {
		// observe and the downstream write run on the same upstream reader.
		// Keep this control event's A even if the client submits another update.
		return m.observedControlModel
	}
	if strings.HasPrefix(eventType, "session.") {
		return m.session.publicModel()
	}
	id := openAIWSClientResponseID(payload)
	if model, ok := m.responses[id]; ok {
		return model
	}
	if m.active != nil && (id == "" || m.active.responseID == "" || m.active.responseID == id) {
		return m.active.model
	}
	// A response whose ownership is unknown must never fall back to B/C or to
	// an unrelated turn. The shared rewriter rejects only controlled identity
	// fields in that case; ordinary deltas without model fields still pass.
	return ""
}

func (m *openAIWSClientModels) isUnrelatedResponse(payload []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := openAIWSClientResponseID(payload)
	_, completed := m.responses[id]
	return id != "" && (completed || m.active == nil || (m.active.responseID != "" && m.active.responseID != id))
}

func (m *openAIWSClientModels) ownsTerminal(payload []byte) bool {
	if !openAIWSPassthroughIsTerminalOutput(payload) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := openAIWSClientResponseID(payload)
	return m.active != nil && (id == "" || m.active.responseID == id)
}

func (m *openAIWSClientModels) markSettled(responseID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil && (responseID == "" || m.active.responseID == responseID) {
		m.active.settled = true
	}
}

func (m *openAIWSClientModels) finishTerminal(payload []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := openAIWSClientResponseID(payload)
	if m.active != nil && (id == "" || m.active.responseID == id) {
		if m.active.responseID != "" {
			if m.responses == nil {
				m.responses = make(map[string]string)
			}
			m.responses[m.active.responseID] = m.active.model
		}
		m.active = nil
	}
}

func (m *openAIWSClientModels) partialTurn() *openAIWSClientModelTurn {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.settled {
		return nil
	}
	turn := *m.active
	return &turn
}

func openAIWSClientJSONFrame(msgType coderws.MessageType, payload []byte) bool {
	if msgType == coderws.MessageText {
		return true
	}
	if msgType != coderws.MessageBinary {
		return false
	}
	trimmed := bytes.TrimSpace(payload)
	// Preserve opaque binary frames, but do not let malformed JSON objects
	// bypass the same handling as a text protocol event.
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

func openAIWSClientPayloadFailureEvent(responseID string) []byte {
	if responseID == "" {
		return []byte(`{"type":"error","error":{"type":"server_error","code":"upstream_error","message":"Upstream request failed"}}`)
	}
	body, _ := json.Marshal(map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id": responseID, "object": "response", "status": "failed", "output": []any{},
			"error": map[string]any{"type": "server_error", "code": "upstream_error", "message": "Upstream request failed"},
		},
	})
	return body
}

func openAIWSClientPolicyBlockedError(account *Account, blocked *OpenAIFastBlockedError) *OpenAIFastBlockedError {
	if blocked == nil || !openAIClientPrivacyApplies(account) {
		return blocked
	}
	return &OpenAIFastBlockedError{
		Message: OpenAIClientErrorMessage(http.StatusForbidden, []byte(`{"error":{"code":"policy_violation"}}`), blocked.Message),
	}
}
