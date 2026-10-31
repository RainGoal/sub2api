package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const configuredAccountModelErrorBody = `{"error":{"code":"upstream_error","message":"Model \"deepseek-v4-pro\" is not supported by any configured account in this group"}}`

func TestConfiguredAccount404Failover(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want bool
	}{
		{"reported error with non-model code", configuredAccountModelErrorBody, true},
		{"case insensitive", `{"error":{"message":"No CONFIGURED ACCOUNT supports this model"}}`, true},
		{"plain text", "No configured account supports this model", true},
		{"detail", `{"detail":"No configured account supports this model"}`, true},
		{"nested response error", `{"response":{"error":{"message":"No configured account supports this model"}}}`, true},
		{"nested detail message", `{"detail":{"message":"No configured account supports this model"}}`, true},
		{"ordinary path error", `{"error":{"message":"Unknown request URL"}}`, false},
		{"echoed request", `{"error":{"message":"Invalid request"},"echo":"configured account"}`, false},
		{"object detail echo", `{"detail":{"request":"configured account"}}`, false},
		{"configured alone", `{"error":{"message":"Model is not configured"}}`, false},
		{"account alone", `{"error":{"message":"Account not available"}}`, false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{accountRepo: &modelNotFoundManagedAccountRepo{}}
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			require.Equal(t, tt.want, svc.shouldFailoverOpenAIUpstreamResponse(account, http.StatusNotFound, "", []byte(tt.body)))
		})
	}
}

func TestConfiguredAccount404NeverRetriesSameAccount(t *testing.T) {
	err := newOpenAIUpstreamFailoverError(http.StatusNotFound, http.Header{}, []byte(configuredAccountModelErrorBody), "", true)
	require.False(t, err.RetryableOnSameAccount)
	require.False(t, err.RequestScopedTransient)
	require.True(t, err.ShouldRetryNextAccount())
}

func TestConfiguredAccount404PassthroughWithoutPool(t *testing.T) {
	for _, tt := range []struct {
		name     string
		platform string
		body     string
		want     bool
	}{
		{"OpenAI model unavailable", PlatformOpenAI, configuredAccountModelErrorBody, true},
		{"compatible provider model unavailable", PlatformDeepseek, configuredAccountModelErrorBody, true},
		{"ordinary 404 unchanged", PlatformOpenAI, `{"error":{"message":"Unknown request URL"}}`, false},
		{"other platform unchanged", PlatformAnthropic, configuredAccountModelErrorBody, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{Platform: tt.platform, Type: AccountTypeAPIKey}
			require.False(t, account.IsPoolMode())
			require.Equal(t, tt.want, shouldFailoverOpenAIPassthroughResponse(account, http.StatusNotFound, []byte(tt.body)))
		})
	}
}

func TestConfiguredAccount404FailoverScope(t *testing.T) {
	for _, tt := range []struct {
		name     string
		platform string
		status   int
		managed  bool
		want     bool
	}{
		{"managed OpenAI", PlatformOpenAI, 404, true, true},
		{"managed compatible provider", PlatformDeepseek, 404, true, true},
		{"direct forwarding", PlatformOpenAI, 404, false, false},
		{"Anthropic unchanged", PlatformAnthropic, 404, true, false},
		{"Gemini unchanged", PlatformGemini, 404, true, false},
		{"400 unchanged", PlatformOpenAI, 400, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			if tt.managed {
				svc.accountRepo = &modelNotFoundManagedAccountRepo{}
			}
			account := &Account{Platform: tt.platform, Type: AccountTypeAPIKey}
			require.Equal(t, tt.want, svc.shouldFailoverOpenAIUpstreamResponse(account, tt.status, "", []byte(configuredAccountModelErrorBody)))
		})
	}
}
