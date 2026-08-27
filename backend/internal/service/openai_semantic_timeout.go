package service

import (
	"strings"
)

// openAIProviderTimeoutMarkers are emitted by some OpenAI-compatible upstreams
// as a normal HTTP 200 response even though the request failed upstream.
// Keep this rule deliberately narrow so ordinary long-running responses are
// unaffected and the workaround can be removed independently later.
var openAIProviderTimeoutMarkers = [...]string{
	"request exceeded 100s limit:",
	"request exceeded 300s limit:",
}

const openAIProviderTimeoutMessage = "Request exceeded 100s limit"

func isOpenAIProviderTimeout(account *Account, usage OpenAIUsage, body []byte) bool {
	return isOpenAIProviderTimeoutText(account, usage, string(body))
}

func isOpenAIProviderTimeoutText(account *Account, usage OpenAIUsage, text string) bool {
	if account == nil || account.Platform != PlatformOpenAI {
		return false
	}
	if usage.CacheReadInputTokens != 1000 || usage.OutputTokens != 1000 {
		return false
	}
	return openAIProviderTimeoutMarkerInText(text)
}

// openAIProviderTimeoutMarkerInText normalizes case and whitespace so the
// marker still matches when JSON/SSE framing splits or wraps the message.
func openAIProviderTimeoutMarkerInText(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, marker := range openAIProviderTimeoutMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// openAIProviderTimeoutProbe keeps only a small rolling tail for streaming
// responses. The marker is short, so this handles chunk boundaries without
// retaining the complete response body.
type openAIProviderTimeoutProbe struct {
	tail    string
	matched bool
}

func (p *openAIProviderTimeoutProbe) Observe(text string) {
	if p == nil || text == "" {
		return
	}
	p.tail += " " + text
	if openAIProviderTimeoutMarkerInText(p.tail) {
		p.matched = true
	}
	if len(p.tail) > 512 {
		p.tail = p.tail[len(p.tail)-512:]
	}
}

func (p *openAIProviderTimeoutProbe) Matched() bool {
	return p != nil && (p.matched || openAIProviderTimeoutMarkerInText(p.tail))
}
