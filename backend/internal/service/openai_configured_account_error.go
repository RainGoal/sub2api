package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// isOpenAIConfiguredAccountModelError recognizes a provider's deterministic
// model availability error. Structured payloads may echo request content, so
// only explicit string error messages are authoritative.
func isOpenAIConfiguredAccountModelError(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	match := func(message string) bool {
		return strings.Contains(strings.ToLower(message), "configured account")
	}
	if !gjson.ValidBytes(body) {
		return match(string(body))
	}
	for _, path := range []string{"error.message", "response.error.message", "detail.message", "detail", "message"} {
		message := gjson.GetBytes(body, path)
		if message.Type == gjson.String && match(message.String()) {
			return true
		}
	}
	return false
}

// IsOpenAIConfiguredAccountModelError identifies the model availability error
// whose final presentation is deferred until account failover is exhausted.
func (e *UpstreamFailoverError) IsOpenAIConfiguredAccountModelError() bool {
	return e != nil && isOpenAIConfiguredAccountModelError(e.StatusCode, e.ResponseBody)
}
