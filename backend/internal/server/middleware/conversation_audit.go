package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/conversationaudit"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const conversationAuditContextKey = "sub2api.conversation_audit"

type conversationAuditState struct {
	session   conversationaudit.Session
	protocol  string
	errorCode string
	mu        sync.Mutex
}

type conversationAuditResponseWriter struct {
	gin.ResponseWriter
	state          *conversationAuditState
	binaryObserved bool
}

type effectiveConversationAuditRecorder interface {
	conversationaudit.Recorder
	EffectiveEnabled() bool
}

type conversationAuditRoute struct {
	protocol string
	endpoint string
}

func beginConversationAudit(c *gin.Context, recorder conversationaudit.Recorder, apiKey *service.APIKey) func() {
	effective, ok := recorder.(effectiveConversationAuditRecorder)
	if !ok || c == nil || c.Request == nil || apiKey == nil {
		return nil
	}
	enabled := false
	safelyRunConversationAudit(func() { enabled = effective.EffectiveEnabled() })
	if !enabled {
		return nil
	}
	route, ok := classifyConversationAuditRoute(c.Request.Method, c.Request.URL.Path)
	if !ok {
		return nil
	}
	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	if strings.TrimSpace(requestID) == "" {
		requestID, _ = c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	}
	if strings.TrimSpace(requestID) == "" {
		requestID = "audit:" + uuid.NewString()
	}
	userName := ""
	userID := apiKey.UserID
	if apiKey.User != nil {
		if userID <= 0 {
			userID = apiKey.User.ID
		}
		userName = strings.TrimSpace(apiKey.User.Username)
		if userName == "" {
			userName = strings.TrimSpace(apiKey.User.Email)
		}
	}
	var session conversationaudit.Session
	safelyRunConversationAudit(func() {
		session = effective.Begin(c.Request.Context(), conversationaudit.BeginInput{
			RequestID: requestID, SessionID: service.ExtractClientSessionID(c),
			UserID: userID, UserName: userName, APIKeyID: apiKey.ID, APIKeyName: apiKey.Name,
			Protocol: route.protocol, InboundEndpoint: route.endpoint, TransportMode: conversationaudit.TransportHTTP,
		})
	})
	if session == nil {
		return nil
	}
	patch := conversationaudit.MetadataPatch{}
	if apiKey.GroupID != nil {
		value := *apiKey.GroupID
		patch.GroupID = &value
	}
	if apiKey.Group != nil {
		patch.GroupName = apiKey.Group.Name
	}
	safelyRunConversationAudit(func() { session.Annotate(patch) })
	state := &conversationAuditState{session: session, protocol: route.protocol}
	c.Set(conversationAuditContextKey, state)
	c.Writer = &conversationAuditResponseWriter{ResponseWriter: c.Writer, state: state}

	return func() {
		recovered := recover()
		finishConversationAudit(c, state, recovered != nil)
		if recovered != nil {
			panic(recovered)
		}
	}
}

// CaptureConversationAuditRequest shares a body already read by the gateway.
// Audit failures are swallowed locally and never affect Prompt Audit decisions.
func CaptureConversationAuditRequest(c *gin.Context, protocol, model string, body []byte) {
	state := conversationAuditStateFromContext(c)
	if state == nil {
		return
	}
	safelyRunConversationAudit(func() {
		state.mu.Lock()
		if strings.TrimSpace(protocol) != "" {
			state.protocol = protocol
		}
		state.mu.Unlock()
		state.session.Annotate(conversationaudit.MetadataPatch{RequestedModel: strings.TrimSpace(model)})
		state.session.SetRequestBody(protocol, body)
	})
}

// MarkConversationAuditError preserves stable local error codes for failures
// that do not produce a parseable response payload.
func MarkConversationAuditError(c *gin.Context, code string) {
	state := conversationAuditStateFromContext(c)
	if state == nil {
		return
	}
	state.mu.Lock()
	if state.errorCode == "" {
		state.errorCode = strings.TrimSpace(code)
	}
	state.mu.Unlock()
}

func (w *conversationAuditResponseWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	if n > 0 {
		w.observe(body[:n])
	}
	return n, err
}

func (w *conversationAuditResponseWriter) WriteString(body string) (int, error) {
	n, err := w.ResponseWriter.WriteString(body)
	if n > 0 {
		w.observe([]byte(body[:n]))
	}
	return n, err
}

func (w *conversationAuditResponseWriter) observe(body []byte) {
	contentType := strings.ToLower(w.Header().Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "json") &&
		!strings.Contains(contentType, "text/") && !strings.Contains(contentType, "event-stream") {
		if !w.binaryObserved {
			w.binaryObserved = true
			safelyRunConversationAudit(func() {
				w.state.session.Observe(conversationaudit.ResponseEvent{Message: conversationaudit.Message{
					Role: "assistant", Content: []conversationaudit.ContentItem{{Type: "media_omitted", MediaType: contentType}},
				}})
			})
		}
		return
	}
	w.state.mu.Lock()
	protocol := w.state.protocol
	w.state.mu.Unlock()
	safelyRunConversationAudit(func() { w.state.session.ObserveResponseBytes(protocol, body) })
}

func finishConversationAudit(c *gin.Context, state *conversationAuditState, panicked bool) {
	if state == nil {
		return
	}
	safelyRunConversationAudit(func() {
		state.mu.Lock()
		errorCode := state.errorCode
		state.mu.Unlock()
		status := c.Writer.Status()
		outcome := conversationaudit.OutcomeCompleted
		if panicked {
			status = http.StatusInternalServerError
			outcome = conversationaudit.OutcomeError
			errorCode = "panic"
		} else if streamErr, ok := service.GetOpsStreamError(c); ok {
			outcome = conversationaudit.OutcomeError
			if c.Writer.Size() > 0 && status < http.StatusBadRequest {
				outcome = conversationaudit.OutcomePartial
			}
			if errorCode == "" {
				errorCode = strings.TrimSpace(streamErr.Code)
				if errorCode == "" {
					errorCode = strings.TrimSpace(streamErr.ErrType)
				}
			}
		} else if requestErr := c.Request.Context().Err(); requestErr != nil {
			if c.Writer.Size() > 0 && status < http.StatusBadRequest {
				outcome = conversationaudit.OutcomePartial
			} else if requestErr == context.DeadlineExceeded {
				outcome = conversationaudit.OutcomeTimeout
			} else {
				outcome = conversationaudit.OutcomeCancelled
			}
			if errorCode == "" {
				errorCode = requestErr.Error()
			}
		} else if status >= http.StatusBadRequest {
			outcome = conversationaudit.OutcomeError
		}
		patch := conversationaudit.MetadataPatch{}
		if accountID, ok := c.Request.Context().Value(ctxkey.AccountID).(int64); ok && accountID > 0 {
			patch.AccountID = &accountID
		}
		if value, ok := c.Get(service.OpsUpstreamModelKey); ok {
			patch.EffectiveModel, _ = value.(string)
		}
		if strings.Contains(strings.ToLower(c.Writer.Header().Get("Content-Type")), "event-stream") {
			patch.TransportMode = conversationaudit.TransportSSE
		}
		state.session.Annotate(patch)
		state.session.Finish(conversationaudit.FinishResult{
			OutcomeStatus: outcome, HTTPStatus: status, ErrorCode: errorCode,
		})
	})
}

func conversationAuditStateFromContext(c *gin.Context) *conversationAuditState {
	if c == nil {
		return nil
	}
	value, ok := c.Get(conversationAuditContextKey)
	if !ok {
		return nil
	}
	state, _ := value.(*conversationAuditState)
	return state
}

func safelyRunConversationAudit(run func()) {
	defer func() { _ = recover() }()
	if run != nil {
		run()
	}
}

func classifyConversationAuditRoute(method, path string) (conversationAuditRoute, bool) {
	if method != http.MethodPost {
		return conversationAuditRoute{}, false
	}
	path = strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	switch {
	case strings.Contains(path, "/messages/count_tokens"):
		return conversationAuditRoute{}, false
	case strings.Contains(path, "/chat/completions"):
		return conversationAuditRoute{protocol: "openai_chat", endpoint: "/v1/chat/completions"}, true
	case strings.Contains(path, "/messages"):
		return conversationAuditRoute{protocol: "anthropic_messages", endpoint: "/v1/messages"}, true
	case strings.Contains(path, "/responses/input_tokens"):
		return conversationAuditRoute{}, false
	case strings.Contains(path, "/responses/compact"):
		return conversationAuditRoute{protocol: "openai_responses", endpoint: "/v1/responses/compact"}, true
	case strings.Contains(path, "/responses"):
		return conversationAuditRoute{protocol: "openai_responses", endpoint: "/v1/responses"}, true
	case strings.Contains(path, "/embeddings"):
		return conversationAuditRoute{protocol: "embeddings", endpoint: "/v1/embeddings"}, true
	case strings.Contains(path, "/v1beta/models/") && (strings.Contains(path, ":generatecontent") || strings.Contains(path, ":streamgeneratecontent")):
		return conversationAuditRoute{protocol: "gemini", endpoint: "/v1beta/models"}, true
	case strings.Contains(path, "/images/generations"):
		return conversationAuditRoute{protocol: "openai_images", endpoint: "/v1/images/generations"}, true
	case strings.Contains(path, "/images/edits"):
		return conversationAuditRoute{protocol: "openai_images", endpoint: "/v1/images/edits"}, true
	case strings.Contains(path, "/videos") && !strings.Contains(path, "/content"):
		return conversationAuditRoute{protocol: "video", endpoint: "/v1/videos"}, true
	case strings.HasSuffix(path, "/tts"), strings.HasSuffix(path, "/stt"):
		return conversationAuditRoute{protocol: "openai_audio", endpoint: "/v1/audio"}, true
	case strings.HasSuffix(path, "/web_search"), strings.HasSuffix(path, "/x_search"), strings.Contains(path, "/alpha/search"):
		return conversationAuditRoute{protocol: "openai_search", endpoint: "/v1/search"}, true
	default:
		return conversationAuditRoute{}, false
	}
}
