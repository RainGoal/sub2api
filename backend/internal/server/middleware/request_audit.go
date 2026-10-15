package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type requestAuditStateKey struct{}

const requestAuditUserAgentKey = "request_audit_original_user_agent"

type requestAuditUserAgent struct {
	values  []string
	present bool
}

// Consume the only header rewritten by the global middleware before gateway
// audit runs. Do not retain an unbudgeted snapshot for the lifetime of a WS.
func requestAuditInboundHeaders(c *gin.Context, capture bool) http.Header {
	value, saved := c.Get(requestAuditUserAgentKey)
	if !saved {
		return c.Request.Header
	}
	c.Set(requestAuditUserAgentKey, nil)
	original, ok := value.(requestAuditUserAgent)
	if !capture || !ok {
		return c.Request.Header
	}
	headers := make(http.Header, len(c.Request.Header))
	for name, values := range c.Request.Header {
		headers[name] = values
	}
	if original.present {
		headers["User-Agent"] = original.values
	} else {
		delete(headers, "User-Agent")
	}
	return headers
}

type requestAuditState struct {
	ingress           *requestaudit.Ingress
	started           time.Time
	checkedSession    string
	checkedDevice     string
	pendingSession    string
	pendingSource     string
	pendingResolution string
}

// RequestAuditBegin snapshots the client request before authentication and
// wraps the original stream. It never blocks the request on PostgreSQL.
func RequestAuditBegin(manager *requestaudit.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}
		headers := requestAuditInboundHeaders(c, manager.Enabled())
		if manager == nil {
			c.Next()
			return
		}
		meta := requestaudit.RequestMeta{
			Kind: "http_request", ReceivedAt: time.Now(), Method: c.Request.Method,
			RoutePattern: c.FullPath(), Protocol: c.Request.Proto, Host: c.Request.Host,
			RequestURI: c.Request.RequestURI, ContentLength: c.Request.ContentLength,
			TransferEncoding: append([]string(nil), c.Request.TransferEncoding...),
			Headers:          headers, HeaderSource: "inbound",
		}
		if strings.EqualFold(c.Request.Header.Get("Upgrade"), "websocket") {
			meta.Kind = "websocket_handshake"
		}
		if id, ok := c.Request.Context().Value(ctxkey.ClientRequestID).(string); ok {
			meta.ClientRequestID = id
		}
		ingress := requestaudit.BeginIngress(manager, meta)
		state := &requestAuditState{ingress: ingress, started: time.Now()}
		ctx := context.WithValue(c.Request.Context(), requestAuditStateKey{}, state)
		c.Request = c.Request.WithContext(requestaudit.WithIngress(ctx, ingress))
		if ingress != nil && ingress.Capture() != nil {
			request := c.Request
			if request.Body == nil || request.Body == http.NoBody {
				ingress.Capture().EndPart(requestaudit.PartBody, true, "")
			} else {
				request.Body = ingress.Capture().WrapBodyWithTrailers(request.Body, func() http.Header { return request.Trailer })
			}
		}
		defer func() {
			recovered := recover()
			if ingress != nil {
				state.bindPendingSession()
				outcome := "completed"
				status := c.Writer.Status()
				if recovered != nil {
					outcome = "panic"
					if !c.Writer.Written() {
						status = http.StatusInternalServerError
					}
				} else if c.Request.Context().Err() != nil {
					outcome = "canceled"
				} else if status >= 400 {
					outcome = "failed"
				}
				ingress.Finish(status, state.started, outcome, c.Request.Trailer)
			}
			if recovered != nil {
				panic(recovered)
			}
		}()
		c.Next()
	}
}

func requestAuditStateFromContext(ctx context.Context) *requestAuditState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(requestAuditStateKey{}).(*requestAuditState)
	return state
}

// RequestAuditAfterAuth binds the authenticated API key and checks an exact
// API-key/session ban before handlers perform upstream selection.
func RequestAuditAfterAuth(protocol string, normalizedBodyLimit ...int64) gin.HandlerFunc {
	var maxNormalizedBytes int64
	if len(normalizedBodyLimit) > 0 {
		maxNormalizedBytes = normalizedBodyLimit[0]
	}
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}
		state := requestAuditStateFromContext(c.Request.Context())
		if state == nil || state.ingress == nil {
			c.Next()
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil {
			c.Next()
			return
		}
		bindRequestAuditIdentity(c, state, apiKey)
		if requestAuditCheckHTTPBan(c, state, protocol) {
			return
		}
		c.Request = c.Request.WithContext(httputil.WithRequestBodyObserver(c.Request.Context(), func(body []byte) error {
			return requestAuditObserveBody(c, state, body, protocol, maxNormalizedBytes)
		}))
		c.Next()
	}
}

// RequestAuditAfterRouting resolves protocol-specific header fallbacks which
// only become known after a composite model route has selected its platform.
func RequestAuditAfterRouting(protocol string) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := requestAuditStateFromContext(c.Request.Context())
		if state != nil && state.ingress != nil {
			if key, ok := GetAPIKeyFromContext(c); ok {
				bindRequestAuditIdentity(c, state, key)
			}
			_, resolved := service.ResolvedTargetPlatformFromContext(c.Request.Context())
			// Responses WS resolves composite routing from its first frame, after
			// this handshake middleware. Keep metadata behind the Grok header.
			awaitFirstFrame := !resolved && c.Request.Method == http.MethodGet &&
				strings.Contains(c.Request.URL.Path, "/responses") && strings.EqualFold(c.GetHeader("Upgrade"), "websocket")
			if !awaitFirstFrame {
				state.bindPendingSession()
			}
			if requestAuditCheckHTTPBan(c, state, protocol) {
				return
			}
		}
		c.Next()
	}
}

func requestAuditObserveBody(c *gin.Context, state *requestAuditState, body []byte, protocol string, maxNormalizedBytes int64) error {
	if state == nil || state.ingress == nil {
		return nil
	}
	ingress := state.ingress
	if ingress.DeviceTrackingEnabled() && ingress.Identity().DeviceID == "" {
		isClaude := strings.Contains(c.Request.URL.Path, "/messages")
		if isClaude || strings.Contains(c.Request.URL.Path, "/responses") {
			deviceType, deviceID, source, resolution := service.RequestAuditBodyDevice(body, isClaude, maxNormalizedBytes)
			ingress.SetDevice(deviceType, deviceID, source, resolution)
		}
	}
	model := gjson.GetBytes(body, "model")
	if model.Type != gjson.String {
		model = gjson.GetBytes(body, "session.model")
	}
	if model.Type == gjson.String {
		ingress.SetModel(strings.TrimSpace(model.String()))
	}
	if ingress.Identity().SessionID == "" && strings.Contains(c.Request.URL.Path, "/messages") {
		if sessionID := service.ClaudeMetadataSessionID(body, maxNormalizedBytes); sessionID != "" {
			state.bindMetadataSession(c, sessionID, "body.metadata.user_id", "claude_metadata")
		}
	}
	if ingress.Identity().SessionID == "" && strings.Contains(c.Request.URL.Path, "/responses") {
		if sessionID, source, resolution := service.CodexMetadataSessionID(body, maxNormalizedBytes); sessionID != "" {
			state.bindMetadataSession(c, sessionID, source, resolution)
		}
	}
	if requestAuditCheckHTTPBan(c, state, protocol) {
		return httputil.ErrBodyObserverRejected
	}
	return nil
}

func (state *requestAuditState) bindMetadataSession(c *gin.Context, sessionID, source, resolution string) {
	// Composite routing can still reveal a higher-priority Grok header. Keep
	// only the first bounded metadata candidate (header before body) until then.
	key, _ := GetAPIKeyFromContext(c)
	_, resolved := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	if !resolved && key != nil && key.Group != nil && key.Group.Platform == service.PlatformComposite && c.GetHeader("x-grok-conv-id") != "" {
		if state.pendingSession == "" {
			state.pendingSession, state.pendingSource, state.pendingResolution = sessionID, source, resolution
		}
		return
	}
	state.ingress.SetSession(sessionID, source, resolution)
}

func (state *requestAuditState) bindPendingSession() {
	if state.pendingSession != "" {
		state.ingress.SetSession(state.pendingSession, state.pendingSource, state.pendingResolution)
		state.pendingSession, state.pendingSource, state.pendingResolution = "", "", ""
	}
}

func requestAuditCheckHTTPBan(c *gin.Context, state *requestAuditState, protocol string) bool {
	identity := state.ingress.Identity()
	device := identity.DeviceType + ":" + identity.DeviceID
	if identity.DeviceID != "" && state.checkedDevice != device {
		state.checkedDevice = device
		if ban, err := state.ingress.CheckDeviceBan(c.Request.Context()); err == nil && ban != nil {
			writeRequestAuditPolicyBan(c, protocol, "device_banned", ban.PublicReason)
			return true
		}
	}
	session := identity.SessionID
	if session == "" || state.checkedSession == session {
		return false
	}
	state.checkedSession = session
	// One admission decision per resolved HTTP session, including fail-open.
	// Upstream retries must not recheck and cancel an already admitted request.
	ban, err := state.ingress.CheckBan(c.Request.Context())
	if err != nil || ban == nil {
		return false
	}
	writeRequestAuditBan(c, protocol, ban)
	return true
}

func bindRequestAuditIdentity(c *gin.Context, state *requestAuditState, key *service.APIKey) {
	ingress := state.ingress
	if ingress == nil || key == nil {
		return
	}
	var groupID int64
	if key.GroupID != nil {
		groupID = *key.GroupID
	}
	ingress.BindIdentity(key.UserID, key.ID, groupID)
	// Messages identifies Claude clients from metadata.user_id. An unrelated
	// Codex header must not suppress that protocol's original device identity.
	if ingress.DeviceTrackingEnabled() && !strings.Contains(c.Request.URL.Path, "/messages") {
		deviceType, deviceID, deviceSource, deviceResolution := service.RequestAuditHeaderDevice(c.Request.Header)
		ingress.SetDevice(deviceType, deviceID, deviceSource, deviceResolution)
	}
	if c.Request.Method == http.MethodGet {
		ingress.SetModel(strings.TrimSpace(c.Query("model")))
	}
	sessionID, source, resolution := service.ExtractRequestAuditSessionID(c)
	if source == "header.x-codex-turn-metadata.session_id" {
		state.bindMetadataSession(c, sessionID, source, resolution)
	} else {
		ingress.SetSession(sessionID, source, resolution)
	}
}

func writeRequestAuditBan(c *gin.Context, protocol string, ban *requestaudit.SessionBan) {
	reason := "session blocked"
	if ban != nil && strings.TrimSpace(ban.PublicReason) != "" {
		reason = strings.TrimSpace(ban.PublicReason)
	}
	writeRequestAuditPolicyBan(c, protocol, "session_banned", reason)
}

func writeRequestAuditPolicyBan(c *gin.Context, protocol, code, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "device blocked"
	}
	if strings.HasPrefix(protocol, "anthropic") || strings.Contains(c.Request.URL.Path, "/messages") {
		c.JSON(http.StatusForbidden, gin.H{"type": "error", "error": gin.H{"type": "permission_error", "message": reason}})
	} else if strings.HasPrefix(protocol, "google") || strings.HasPrefix(c.Request.URL.Path, "/v1beta") {
		GoogleErrorWriter(c, http.StatusForbidden, reason)
	} else {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "code": code, "message": reason}})
	}
	c.Abort()
}

// IsRequestAuditBodyObserverError lets existing reader branches preserve the
// already-written ban response rather than replacing it with a 400/413.
func IsRequestAuditBodyObserverError(err error) bool {
	return errors.Is(err, httputil.ErrBodyObserverRejected)
}
