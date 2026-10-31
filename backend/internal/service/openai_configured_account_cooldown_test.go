//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredAccount404CooldownUsesCanonicalModel(t *testing.T) {
	for _, customExcludes404 := range []bool{false, true} {
		name := "model cooldown"
		if customExcludes404 {
			name = "custom error codes skip cooldown but still fail over"
		}
		t.Run(name, func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			account := openAIModelNotFoundTempAccount()
			account.Credentials["model_mapping"] = map[string]any{"public-a": "upstream-a", "upstream-a": "upstream-b"}
			account.Credentials["pool_mode"] = true
			if customExcludes404 {
				account.Credentials["custom_error_codes_enabled"] = true
				account.Credentials["custom_error_codes"] = []any{float64(500)}
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, rateLimitService: &RateLimitService{accountRepo: repo}}
			c, rec := newOpenAIUpstreamErrorTestContext(t)
			body := []byte(configuredAccountModelErrorBody)
			err := svc.failoverOpenAIUpstreamHTTPError(context.Background(), c, account, newOpenAIUpstreamErrorResponse(http.StatusNotFound, string(body)), body, "", "upstream-a")
			require.NotNil(t, err)
			require.True(t, err.ShouldRetryNextAccount())
			require.False(t, err.RetryableOnSameAccount)
			require.False(t, IsResponseCommitted(c))
			require.Empty(t, rec.Body.String())
			require.Zero(t, repo.tempCalls)
			if customExcludes404 {
				require.Empty(t, repo.modelRateLimitCalls)
				return
			}
			require.Len(t, repo.modelRateLimitCalls, 1)
			call := repo.modelRateLimitCalls[0]
			require.Equal(t, account.ID, call.accountID)
			require.Equal(t, "upstream-a", call.scope, "do not map an already canonical model twice")
			require.Equal(t, upstreamModelNotFoundReason, call.reason)
			require.WithinDuration(t, time.Now().Add(30*time.Minute), call.resetAt, 5*time.Second)
			account.Extra = map[string]any{modelRateLimitsKey: map[string]any{call.scope: map[string]any{"rate_limit_reset_at": call.resetAt.UTC().Format(time.RFC3339)}}}
			require.False(t, account.IsSchedulableForModelWithContext(context.Background(), "public-a"))
			require.True(t, account.IsSchedulableForModelWithContext(context.Background(), "other-model"))
			other := *account
			other.ID++
			other.Extra = nil
			require.True(t, other.IsSchedulableForModelWithContext(context.Background(), "public-a"))
		})
	}
}
