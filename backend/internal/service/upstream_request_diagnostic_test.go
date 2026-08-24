package service

import (
	"net/http"
	"strings"
	"testing"
)

func TestUpstreamRequestDiagnosticRedactsSecretsAndKeepsClaudeIdentity(t *testing.T) {
	headers := http.Header{
		"User-Agent":                  {"claude-cli/2.1.220 (external, cli)"},
		"Anthropic-Beta":              {"claude-code-20250219"},
		"X-Api-Key":                   {"sk-ant-secret-value"},
		"Authorization":               {"Bearer secret-value"},
		ChannelMonitorProbeHeaderName: {"v1.timestamp.signature"},
	}
	body := []byte(`{"model":"claude-test","api_key":"body-secret","messages":[{"role":"user","content":"hi"}]}`)

	gotHeaders := diagnosticHeaders(headers)
	gotBody := diagnosticBody(body)
	gotURL := diagnosticURL("https://example.com/v1/messages?beta=true&api_key=query-secret")
	combined := gotHeaders + gotBody + gotURL

	for _, secret := range []string{"sk-ant-secret-value", "Bearer secret-value", "v1.timestamp.signature", "body-secret", "query-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("diagnostic output leaked %q: %s", secret, combined)
		}
	}
	if !strings.Contains(gotHeaders, "claude-cli/2.1.220") || !strings.Contains(gotHeaders, "claude-code-20250219") {
		t.Fatalf("diagnostic headers lost Claude identity fields: %s", gotHeaders)
	}
	if !strings.Contains(gotURL, "beta=true") {
		t.Fatalf("diagnostic URL lost non-sensitive query: %s", gotURL)
	}
}

func TestMonitorHTTPClientForProviderUsesClaudeFingerprintOnlyForAnthropic(t *testing.T) {
	client, transport := monitorHTTPClientForProvider(MonitorProviderAnthropic)
	if client != monitorAnthropicHTTPClient || transport != "claude_code_tls_fingerprint" {
		t.Fatalf("Anthropic monitor transport = %q, want Claude Code TLS fingerprint", transport)
	}

	client, transport = monitorHTTPClientForProvider(MonitorProviderOpenAI)
	if client != monitorHTTPClient || transport != "go_default_tls" {
		t.Fatalf("OpenAI monitor transport = %q, want default TLS", transport)
	}
}
