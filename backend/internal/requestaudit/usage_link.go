package requestaudit

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"
)

const MaxRequestIDBytes = 256

type usageLinkContextKey struct{}

// This token deliberately does not retain an ingress, request context, headers,
// payload, or capture buffers after an asynchronous usage task is submitted.
type usageLinkToken struct {
	manager  *Manager
	auditID  string
	apiKeyID int64
}

func usageLinkFromContext(ctx context.Context) (usageLinkToken, bool) {
	if ctx == nil {
		return usageLinkToken{}, false
	}
	if token, ok := ctx.Value(usageLinkContextKey{}).(usageLinkToken); ok {
		return token, token.manager != nil && token.auditID != "" && token.apiKeyID > 0
	}
	ingress := IngressFromContext(ctx)
	if ingress == nil {
		return usageLinkToken{}, false
	}
	ingress.mu.Lock()
	defer ingress.mu.Unlock()
	if ingress.capture == nil || ingress.id.APIKeyID <= 0 {
		return usageLinkToken{}, false
	}
	return usageLinkToken{manager: ingress.manager, auditID: ingress.capture.id, apiKeyID: ingress.id.APIKeyID}, true
}

// WithUsageLinkFromContext copies only a captured entry's bounded association
// token onto an independently owned asynchronous usage context.
func WithUsageLinkFromContext(base, parent context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if token, ok := usageLinkFromContext(parent); ok {
		return context.WithValue(base, usageLinkContextKey{}, token)
	}
	return base
}

// LinkUsageRequest associates the final stored usage request ID with the HTTP
// entry or WS handshake. It remains best effort after ingress completion and
// never performs a database operation on the gateway/usage caller's goroutine.
func LinkUsageRequest(ctx context.Context, requestID string, apiKeyID int64) {
	if !ValidRequestID(requestID) {
		return
	}
	token, ok := usageLinkFromContext(ctx)
	if !ok || apiKeyID != token.apiKeyID {
		return
	}
	token.manager.enqueue(auditEvent{kind: "usage_link", id: token.auditID,
		usageRequestID: strings.Clone(requestID), usageAPIKeyID: apiKeyID})
}

// ValidRequestID bounds query and event memory without changing ID identity.
func ValidRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > MaxRequestIDBytes || !utf8.ValidString(requestID) {
		return false
	}
	for _, r := range requestID {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func persistUsageLink(ctx context.Context, tx *sql.Tx, e auditEvent, retention int) error {
	if e.usageAPIKeyID <= 0 || !ValidRequestID(e.usageRequestID) {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO request_audit_usage_links(usage_request_id,api_key_id,audit_id)
SELECT $2,$3,id FROM (SELECT id FROM request_audits WHERE id=$1 AND api_key_id=$3 AND cleanup_state='active'
AND received_at >= NOW() - ($4::int * INTERVAL '1 day') FOR SHARE) parent
ON CONFLICT(usage_request_id,api_key_id,audit_id) DO NOTHING`, e.id, e.usageRequestID, e.usageAPIKeyID, retention)
	return err
}
