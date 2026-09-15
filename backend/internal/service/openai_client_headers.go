package service

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
)

// Apply after the existing allowlist so additional_allowed cannot reintroduce
// model diagnostics. Do not filter the upstream headers used by accounting,
// scheduling, administrator logs, or the Codex continuation-state machinery.
func filterOpenAIClientResponseHeaders(src http.Header, filter *responseheaders.CompiledHeaderFilter) http.Header {
	filtered := responseheaders.FilterHeaders(src, filter)
	removeOpenAIClientDiagnosticHeaders(filtered)
	return filtered
}

func writeOpenAIClientResponseHeaders(dst, src http.Header, filter *responseheaders.CompiledHeaderFilter) {
	if dst == nil {
		return
	}
	for key, values := range filterOpenAIClientResponseHeaders(src, filter) {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	removeOpenAIClientDiagnosticHeaders(dst)
}

func removeOpenAIClientDiagnosticHeaders(headers http.Header) {
	for key := range headers {
		if isOpenAIClientModelDiagnosticHeader(key) || strings.EqualFold(key, "Content-Length") {
			// Delete the actual key: tests and non-net/http upstream clients can
			// supply non-canonical or mixed-case map keys. A rewritten payload's
			// size also no longer agrees with a preexisting upstream length.
			delete(headers, key)
		}
	}
}

func isOpenAIClientModelDiagnosticHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "openai-model", "x-openai-model", "x-openai-model-id", "x-openai-model-name",
		"x-model", "x-model-id", "x-model-name", "x-model-version",
		"x-upstream-model", "x-upstream-model-id", "x-upstream-model-name",
		"x-response-model", "x-response-model-id", "x-served-model", "x-served-model-name", "x-selected-model",
		"x-mapped-model", "x-actual-model", "x-provider-model", "x-backend-model", "x-litellm-model-id":
		return true
	default:
		return false
	}
}
