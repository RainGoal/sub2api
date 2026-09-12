//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountCostFailureDoesNotInterruptUsageBilling(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI} {
		for _, tc := range []struct {
			name    string
			pricing any
		}{
			{"missing_configuration", []ChannelModelPricing{}},
			{"invalid_configuration", map[string]any{"invalid": true}},
			{"wrong_usage_mode", []ChannelModelPricing{{Models: []string{"*"}, BillingMode: BillingModeVideo, PerRequestPrice: testPtrFloat64(0.1)}}},
			{"unmatched_model", []ChannelModelPricing{{Models: []string{"another-model"}, InputPrice: testPtrFloat64(0.1)}}},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				logs := &openAIRecordUsageLogRepoStub{}
				billing := &openAIRecordUsageBillingRepoStub{}
				account := &Account{ID: 71, Platform: platform, Type: AccountTypeAPIKey,
					RateMultiplier: testPtrFloat64(0.5), Extra: map[string]any{AccountModelCostPricingExtraKey: tc.pricing, "quota_limit": 100.0}}
				key, user := &APIKey{ID: 72}, &User{ID: 73}
				var err error
				if platform == PlatformAnthropic {
					svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil)
					err = svc.RecordUsage(context.Background(), &RecordUsageInput{
						Result: &ForwardResult{RequestID: "availability-" + tc.name, Model: "claude-sonnet-4",
							Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 20}}, APIKey: key, User: user, Account: account,
					})
				} else {
					svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
					err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
						Result: &OpenAIForwardResult{RequestID: "availability-" + tc.name, Model: "gpt-5.4",
							Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20}}, APIKey: key, User: user, Account: account,
					})
				}
				require.NoError(t, err)
				require.Equal(t, 1, logs.calls)
				require.Equal(t, 1, billing.calls)
				require.Greater(t, billing.lastCmd.BalanceCost, 0.0)
				require.InDelta(t, logs.lastLog.TotalCost*0.5, billing.lastCmd.AccountQuotaCost, 1e-8)
				require.Nil(t, logs.lastLog.AccountStatsCost, "unavailable purchase costs must not be recorded as free")
				require.Equal(t, 0.5, *logs.lastLog.AccountRateMultiplier)
			})
		}
	}
}
