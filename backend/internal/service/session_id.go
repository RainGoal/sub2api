package service

import (
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// maxPersistedSessionIDLength bounds the persisted client session identifier to the
// usage_logs.session_id column width (VARCHAR(255)). Longer values are rejected so
// distinct identifiers can never alias through truncation.
const maxPersistedSessionIDLength = 255

// clientSessionIDHeaders extends the OpenAI-compatible sticky-session signals with
// native protocol identifiers that are safe to persist but must not alter OpenAI
// scheduling behavior.
var clientSessionIDHeaders = append(
	append([]string(nil), explicitOpenAIHeaderSessionNames...),
	claudeCodeSessionHeader,
)

// ClaudeCodeSessionIDFromHeader returns the stable Claude Code conversation
// identifier carried by X-Claude-Code-Session-Id. It is intentionally exposed
// separately from ExtractClientSessionID: callers that use it for routing must
// make that scope explicit rather than accidentally changing every protocol's
// session semantics.
func ClaudeCodeSessionIDFromHeader(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return sanitizeSessionID(c.GetHeader(claudeCodeSessionHeader))
}

// ExtractClientSessionID resolves the explicit client-provided session identifier from
// request headers for usage-log correlation and returns it sanitized. It is
// protocol-agnostic and shared by every gateway handler so all supported protocols
// record session_id through one seam. Returns "" when no valid identifier is present.
//
// This value feeds only usage_logs.session_id persistence. It does NOT affect sticky
// routing, account selection, request_id semantics, or upstream prompt caching, which
// keep their own (intentionally broader) session-signal resolution.
func ExtractClientSessionID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	for _, header := range clientSessionIDHeaders {
		if sessionID := sanitizeSessionID(c.GetHeader(header)); sessionID != "" {
			return sessionID
		}
	}
	if isGrokRequestContext(c) {
		if sessionID := sanitizeSessionID(c.GetHeader(grokConversationIDHeader)); sessionID != "" {
			return sessionID
		}
	}
	return ""
}

// ExtractRequestAuditSessionID preserves usage correlation semantics while
// exposing the exact selected header and conflicting or invalid input for audit.
// It never substitutes cache keys or server-generated scheduling hashes.
func ExtractRequestAuditSessionID(c *gin.Context) (sessionID, source, resolution string) {
	resolution = "missing"
	if c == nil || c.Request == nil {
		return
	}
	headers := clientSessionIDHeaders
	if isGrokRequestContext(c) {
		headers = append(append([]string(nil), headers...), grokConversationIDHeader)
	}
	for _, header := range headers {
		values := c.Request.Header.Values(header)
		for n, value := range values {
			candidate := sanitizeSessionID(value)
			if candidate == "" {
				if value != "" && sessionID == "" {
					resolution = "invalid"
				}
				continue
			}
			if sessionID == "" && n == 0 {
				sessionID, source, resolution = candidate, "header."+strings.ToLower(header), "resolved"
			} else if sessionID != "" && candidate != sessionID {
				resolution = "conflict"
			}
		}
	}
	return
}

// ClaudeMetadataSessionID returns only a validated explicit Claude conversation
// identifier. It must be called on the first decoded inbound body, before any
// metadata masking or protocol conversion takes place.
func ClaudeMetadataSessionID(body []byte, maxNormalizedBytes int64) string {
	if !gjson.ValidBytes(body) {
		// Match the business reader's BOM/control-byte handling on a parsing
		// copy; the captured inbound body must remain byte-for-byte unchanged.
		// Reuse the business limit so rejected control-byte bodies cannot first
		// allocate an oversized audit-only parsing copy.
		normalized, err := httputil.NormalizeLenientJSONRequestBody(body, maxNormalizedBytes)
		if err != nil || !gjson.ValidBytes(normalized) {
			return ""
		}
		body = normalized
	}
	parsed := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String())
	if parsed == nil {
		return ""
	}
	return sanitizeSessionID(parsed.SessionID)
}

// sanitizeSessionID normalizes a raw client-supplied session identifier for safe
// persistence: it trims surrounding whitespace, rejects the value outright if it
// contains any control character (CR/LF/tab/NUL/…) so a log- or header-injection style
// payload cannot slip into stored correlation data, and rejects values longer than
// the DB column bound. Absent or invalid input yields "".
func sanitizeSessionID(raw string) string {
	if !utf8.ValidString(raw) {
		return ""
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	count := 0
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			// An explicit correlation id never legitimately contains control
			// characters; drop the whole value rather than persist a mangled or
			// partially-injected identifier.
			return ""
		}
		count++
		if count > maxPersistedSessionIDLength {
			return ""
		}
	}
	return trimmed
}
