package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const upstreamDiagnosticPreviewMaxBytes = 16 << 10

var upstreamDiagnosticSensitiveKeys = []string{
	"api_key", "apikey", "key", "token", "secret", "authorization",
}

func logInfoUpstreamRequestDiagnostic(ctx context.Context, event, method, rawURL string, headers http.Header, body []byte, attrs ...any) {
	logUpstreamRequestDiagnostic(ctx, slog.LevelInfo, event, method, rawURL, headers, body, attrs...)
}

func logWarnUpstreamRequestDiagnostic(ctx context.Context, event, method, rawURL string, headers http.Header, body []byte, attrs ...any) {
	logUpstreamRequestDiagnostic(ctx, slog.LevelWarn, event, method, rawURL, headers, body, attrs...)
}

func logUpstreamRequestDiagnostic(ctx context.Context, level slog.Level, event, method, rawURL string, headers http.Header, body []byte, attrs ...any) {
	fields := append([]any{}, attrs...)
	fields = append(fields,
		"method", method,
		"url", diagnosticURL(rawURL),
		"headers", diagnosticHeaders(headers),
		"body_bytes", len(body),
		"body_sha256", diagnosticBodySHA256(body),
		"body", diagnosticBody(body),
	)
	slog.Log(ctx, level, event, fields...)
}

func logInfoUpstreamResponseDiagnostic(ctx context.Context, event string, status int, headers http.Header, body []byte, err error, attrs ...any) {
	logUpstreamResponseDiagnostic(ctx, slog.LevelInfo, event, status, headers, body, err, attrs...)
}

func logWarnUpstreamResponseDiagnostic(ctx context.Context, event string, status int, headers http.Header, body []byte, err error, attrs ...any) {
	logUpstreamResponseDiagnostic(ctx, slog.LevelWarn, event, status, headers, body, err, attrs...)
}

func logUpstreamResponseDiagnostic(ctx context.Context, level slog.Level, event string, status int, headers http.Header, body []byte, err error, attrs ...any) {
	fields := append([]any{}, attrs...)
	fields = append(fields,
		"status", status,
		"headers", diagnosticHeaders(headers),
	)
	if len(body) > 0 {
		fields = append(fields,
			"body_bytes", len(body),
			"body_sha256", diagnosticBodySHA256(body),
			"body", diagnosticBody(body),
		)
	}
	if err != nil {
		fields = append(fields, "error", sanitizeErrorMessage(logredact.RedactText(err.Error(), upstreamDiagnosticSensitiveKeys...)))
	}
	slog.Log(ctx, level, event, fields...)
}

func diagnosticURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return truncateDiagnosticPreview(logredact.RedactText(raw, upstreamDiagnosticSensitiveKeys...))
	}
	query := parsed.Query()
	for key := range query {
		if isDiagnosticSensitiveName(key) {
			query.Set(key, "***")
		}
	}
	parsed.RawQuery = query.Encode()
	return truncateDiagnosticPreview(parsed.String())
}

func diagnosticHeaders(headers http.Header) string {
	if len(headers) == 0 {
		return "{}"
	}
	redacted := make(map[string][]string, len(headers))
	for name, values := range headers {
		if isDiagnosticSensitiveName(name) {
			redacted[name] = []string{"***"}
			continue
		}
		cleanValues := make([]string, 0, len(values))
		for _, value := range values {
			cleanValues = append(cleanValues, truncateDiagnosticPreview(
				sanitizeErrorMessage(logredact.RedactText(value, upstreamDiagnosticSensitiveKeys...)),
			))
		}
		redacted[name] = cleanValues
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "<headers unavailable>"
	}
	return truncateDiagnosticPreview(string(encoded))
}

func diagnosticBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	redacted := logredact.RedactJSON(body, upstreamDiagnosticSensitiveKeys...)
	return truncateDiagnosticPreview(sanitizeErrorMessage(redacted))
}

func diagnosticBodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func isDiagnosticSensitiveName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "api-key") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "cookie") ||
		strings.EqualFold(lower, strings.ToLower(ChannelMonitorProbeHeaderName))
}

func truncateDiagnosticPreview(value string) string {
	if len(value) <= upstreamDiagnosticPreviewMaxBytes {
		return value
	}
	return value[:upstreamDiagnosticPreviewMaxBytes] + "...(truncated)"
}
