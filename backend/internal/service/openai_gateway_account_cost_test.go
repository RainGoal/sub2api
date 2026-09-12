//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRecordUsageAccountPurchaseCost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pricing ChannelModelPricing
		usage   OpenAIUsage
		images  int
		sizes   []string
		want    float64
	}{
		{"tokens_exclude_cache", ChannelModelPricing{Models: []string{"supplier-model"}, BillingMode: BillingModeToken,
			InputPrice: testPtrFloat64(0.001), OutputPrice: testPtrFloat64(0.002),
			CacheReadPrice: testPtrFloat64(0.0001), CacheWritePrice: testPtrFloat64(0.003)},
			OpenAIUsage{InputTokens: 130, OutputTokens: 50, CacheReadInputTokens: 20, CacheCreationInputTokens: 10}, 0, nil, 0.232},
		{"fixed_request_with_three_images", ChannelModelPricing{Models: []string{"supplier-model"}, BillingMode: BillingModePerRequest,
			PerRequestPrice: testPtrFloat64(0.4)}, OpenAIUsage{}, 3, nil, 0.4},
		{"image_tiers", ChannelModelPricing{Models: []string{"supplier-model"}, BillingMode: BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.1), Intervals: []PricingInterval{
				{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0)}, {TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.2)}}},
			OpenAIUsage{}, 3, []string{"1024x1024", "2048x2048", "2048x2048"}, 0.4},
		{"partial_image_dimensions", ChannelModelPricing{Models: []string{"supplier-model"}, BillingMode: BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.1), Intervals: []PricingInterval{
				{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.2)}}},
			OpenAIUsage{}, 3, []string{"2048x2048"}, 0.6},
		{"explicit_free", ChannelModelPricing{Models: []string{"supplier-model"}, BillingMode: BillingModePerRequest,
			PerRequestPrice: testPtrFloat64(0)}, OpenAIUsage{}, 0, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &openAIRecordUsageLogRepoStub{}
			billing := &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
			account := modelCostAccount(PlatformOpenAI, tc.pricing)
			account.Extra["quota_limit"] = 100.0
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "purchase-" + tc.name, Model: "gpt-5.4", UpstreamModel: "supplier-model",
					Usage: tc.usage, ImageCount: tc.images, ImageOutputSizes: tc.sizes},
				APIKey: &APIKey{ID: 501}, User: &User{ID: 601}, Account: account,
				CostOverride: &CostBreakdown{TotalCost: 10, ActualCost: 20, BillingMode: string(BillingModeToken)},
			})
			require.NoError(t, err)
			require.NotNil(t, logs.lastLog)
			require.InDelta(t, tc.want, *logs.lastLog.AccountStatsCost, 1e-12)
			require.Equal(t, 1.0, *logs.lastLog.AccountRateMultiplier)
			require.InDelta(t, tc.want, *billing.lastCmd.SalesCost, 1e-12)
			require.Equal(t, 20.0, billing.lastCmd.BalanceCost)
			require.Equal(t, 2.5, billing.lastCmd.AccountQuotaCost)
		})
	}
}

func TestOpenAIRecordUsagePreservesFrozenAccountCost(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{}
	billing := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
	account := modelCostAccount(PlatformSeedance, ChannelModelPricing{Models: []string{"seedance-2.0"},
		BillingMode: BillingModeVideo, PerRequestPrice: testPtrFloat64(999)})
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "frozen-purchase", Model: "seedance-2.0", VideoCount: 1,
			VideoProvider: PlatformSeedance, VideoDurationSeconds: 10, VideoBillingDurationSeconds: 10},
		APIKey: &APIKey{ID: 501}, User: &User{ID: 601}, Account: account,
		CostOverride:             &CostBreakdown{TotalCost: 1, ActualCost: 2, BillingMode: string(BillingModeVideo)},
		AccountStatsCostOverride: testPtrFloat64(0.4), AccountStatsRateMultiplierOverride: testPtrFloat64(1),
	})
	require.NoError(t, err)
	require.Equal(t, 0.4, *logs.lastLog.AccountStatsCost)
	require.Equal(t, 1.0, *logs.lastLog.AccountRateMultiplier)
	require.Equal(t, 0.4, *billing.lastCmd.SalesCost)
}
