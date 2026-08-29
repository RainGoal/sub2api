package middleware

import (
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// RealtimeConversationAudit maps a long-lived Realtime connection onto
// independent response turns. It only exists when auditing is effectively on.
type RealtimeConversationAudit struct {
	c           *gin.Context
	protocol    string
	model       string
	accountID   int64
	accountName string

	mu         sync.Mutex
	nextTurn   int
	activeTurn int
}

func NewRealtimeConversationAudit(
	c *gin.Context,
	protocol, model, sessionID string,
	accountID int64,
	accountName string,
) *RealtimeConversationAudit {
	state := conversationAuditStateFromContext(c)
	if state == nil || state.recorder == nil {
		return nil
	}
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		return nil
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		state.mu.Lock()
		state.baseInput.SessionID = sessionID
		state.mu.Unlock()
	}
	return &RealtimeConversationAudit{
		c: c, protocol: protocol, model: strings.TrimSpace(model),
		accountID: accountID, accountName: strings.TrimSpace(accountName),
	}
}

// ObserveClientEvent starts a turn only after the event was accepted upstream.
func (a *RealtimeConversationAudit) ObserveClientEvent(payload []byte) {
	if a == nil || len(payload) == 0 {
		return
	}
	safelyRunConversationAudit(func() {
		eventType := realtimeAuditEventType(payload)
		if !realtimeAuditStartsTurn(eventType) {
			return
		}
		a.mu.Lock()
		if a.activeTurn != 0 {
			a.mu.Unlock()
			return
		}
		a.nextTurn++
		turn := a.nextTurn
		a.activeTurn = turn
		a.mu.Unlock()
		BeginConversationAuditTurn(a.c, turn, a.protocol, a.model, payload)
		AnnotateConversationAuditTurn(a.c, turn, a.accountID, a.accountName, a.model)
	})
}

// ObserveServerEvent stores only events successfully written to the client.
// Input transcription can open a Live turn because WebRTC media bypasses the
// sideband connection and therefore has no corresponding client JSON event.
func (a *RealtimeConversationAudit) ObserveServerEvent(payload []byte) {
	if a == nil || len(payload) == 0 {
		return
	}
	safelyRunConversationAudit(func() {
		eventType := realtimeAuditEventType(payload)
		a.mu.Lock()
		turn := a.activeTurn
		startedFromServer := false
		if turn == 0 && realtimeAuditServerStartsTurn(eventType) {
			a.nextTurn++
			turn = a.nextTurn
			a.activeTurn = turn
			startedFromServer = true
		}
		terminal := realtimeAuditIsTerminal(eventType)
		if terminal && turn != 0 {
			a.activeTurn = 0
		}
		a.mu.Unlock()
		if turn == 0 {
			return
		}
		if startedFromServer {
			BeginConversationAuditTurn(a.c, turn, a.protocol, a.model, payload)
			AnnotateConversationAuditTurn(a.c, turn, a.accountID, a.accountName, a.model)
			if realtimeAuditIsInputTranscript(eventType) {
				return
			}
		}
		if realtimeAuditIsInputTranscript(eventType) {
			ObserveConversationAuditTurn(a.c, turn, payload, false)
			return
		}
		ObserveConversationAuditTurn(a.c, turn, payload, terminal)
		if terminal {
			completed := eventType == "response.done" || eventType == "response.completed"
			errorCode := eventType
			if completed {
				errorCode = ""
			}
			FinishConversationAuditTurn(a.c, turn, completed, a.model, errorCode)
		}
	})
}

func realtimeAuditEventType(payload []byte) string {
	return strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "type").String()))
}

func realtimeAuditStartsTurn(eventType string) bool {
	switch eventType {
	case "response.create", "conversation.item.create", "input_audio_buffer.append", "input_audio_buffer.commit":
		return true
	default:
		return false
	}
}

func realtimeAuditIsInputTranscript(eventType string) bool {
	return strings.Contains(eventType, "input_audio_transcription") &&
		(strings.HasSuffix(eventType, ".completed") || strings.HasSuffix(eventType, ".done"))
}

func realtimeAuditServerStartsTurn(eventType string) bool {
	return realtimeAuditIsInputTranscript(eventType) || eventType == "response.created" || eventType == "response.in_progress"
}

func realtimeAuditIsTerminal(eventType string) bool {
	switch eventType {
	case "response.done", "response.completed", "response.failed", "response.incomplete",
		"response.cancelled", "response.canceled", "error", "session.closed", "session.ended":
		return true
	default:
		return false
	}
}
