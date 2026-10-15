package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// AuditOpenAIWSClientFirstMessage binds protocol-dependent headers only after
// the first frame has resolved the target platform, before recording/admission.
func AuditOpenAIWSClientFirstMessage(c *gin.Context, client *coderws.Conn, kind coderws.MessageType, payload []byte) error {
	if c == nil || c.Request == nil {
		return nil
	}
	ctx := c.Request.Context()
	if ingress := requestaudit.IngressFromContext(ctx); ingress != nil {
		if ingress.DeviceTrackingEnabled() {
			deviceType, deviceID, deviceSource, deviceResolution := RequestAuditHeaderDevice(c.Request.Header)
			ingress.SetDevice(deviceType, deviceID, deviceSource, deviceResolution)
		}
		sessionID, source, resolution := ExtractRequestAuditSessionID(c)
		ingress.SetSession(sessionID, source, resolution)
		bindRequestAuditWSMetadataSession(ingress, payload)
		ingress.SetModel(strings.TrimSpace(gjson.GetBytes(payload, "model").String()))
	}
	return auditGatewayWSMessage(ctx, client, kind, payload, true)
}

// Responses has an explicit generation boundary. Cancellation and other control
// messages belonging to an admitted turn must not trigger another policy lookup.
func auditGatewayResponsesWSMessage(ctx context.Context, client *coderws.Conn, kind coderws.MessageType, payload []byte) error {
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	admit := eventType == "" || eventType == "response.create"
	if admit {
		bindRequestAuditWSMetadataSession(requestaudit.IngressFromContext(ctx), payload)
	}
	return auditGatewayWSMessage(ctx, client, kind, payload, admit)
}

func bindRequestAuditWSMetadataSession(ingress *requestaudit.Ingress, payload []byte) {
	if ingress == nil {
		return
	}
	identity := ingress.Identity()
	bindDevice := ingress.DeviceTrackingEnabled() && identity.DeviceID == ""
	if (!bindDevice && identity.SessionID != "") || !utf8.Valid(payload) || !gjson.ValidBytes(payload) {
		return
	}
	if bindDevice {
		deviceType, deviceID, source, resolution := requestAuditBodyDevice(payload, false)
		ingress.SetDevice(deviceType, deviceID, source, resolution)
	}
	if identity.SessionID == "" {
		if sessionID, source, resolution := codexMetadataSessionID(payload); sessionID != "" {
			ingress.SetSession(sessionID, source, resolution)
		}
	}
}

// auditGatewayClientWSMessage runs at the actual client receive boundary,
// before normalizing or forwarding a frame in a transparent message relay.
func auditGatewayClientWSMessage(ctx context.Context, client *coderws.Conn, kind coderws.MessageType, payload []byte) error {
	// Transparent Realtime/Live relays may use server VAD: audio append alone can
	// initiate a turn, so only checking response.create would let bans be bypassed.
	return auditGatewayWSMessage(ctx, client, kind, payload, true)
}

func auditGatewayWSMessage(ctx context.Context, client *coderws.Conn, kind coderws.MessageType, payload []byte, admit bool) error {
	ingress := requestaudit.IngressFromContext(ctx)
	if ingress == nil {
		return nil
	}
	ingress.RecordMessage(kind.String(), payload)
	if !admit {
		return nil
	}
	if ban, err := ingress.CheckDeviceBan(ctx); err == nil && ban != nil {
		return rejectRequestAuditWSBan(ctx, client, payload, "device_banned", ban.PublicReason, errRequestAuditDeviceBlocked)
	}
	ban, err := ingress.CheckBan(ctx)
	if err != nil || ban == nil {
		return nil
	}
	return rejectRequestAuditWSBan(ctx, client, payload, "session_banned", ban.PublicReason, errRequestAuditSessionBlocked)
}

func rejectRequestAuditWSBan(ctx context.Context, client *coderws.Conn, payload []byte, code, publicReason string, blocked error) error {
	reason := strings.TrimSpace(publicReason)
	if reason == "" {
		reason = "This request has been blocked"
	}
	details := map[string]string{
		"type": "permission_error", "code": code, "message": reason,
	}
	event := map[string]any{"type": "error", "error": details}
	if eventID := gjson.GetBytes(payload, "event_id"); eventID.Type == gjson.String && len(eventID.Str) > 0 && len(eventID.Str) <= 255 {
		details["event_id"] = eventID.String()
	}
	message, _ := json.Marshal(event)
	if client != nil {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = client.Write(writeCtx, coderws.MessageText, message)
		cancel()
		_ = client.Close(coderws.StatusPolicyViolation, code)
	}
	return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, code, blocked)
}

var errRequestAuditDeviceBlocked = errors.New("device blocked")

// IsRequestAuditDeviceBlockedError distinguishes local policy from upstream failure.
func IsRequestAuditDeviceBlockedError(err error) bool {
	return errors.Is(err, errRequestAuditDeviceBlocked)
}

var errRequestAuditSessionBlocked = &requestAuditSessionBlockedError{}

type requestAuditSessionBlockedError struct{}

func (*requestAuditSessionBlockedError) Error() string { return "session blocked" }
