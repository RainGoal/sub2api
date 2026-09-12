package service

import (
	"context"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func seedanceCostPrice(value float64) *float64 { return &value }

func seedanceCostGateway() *OpenAIGatewayService {
	return &OpenAIGatewayService{seedanceVideoTaskRepo: &seedanceVideoTaskMemoryRepo{}}
}

func seedanceNativeCostAccount(id int64, prices ...ChannelModelPricing) *Account {
	return &Account{ID: id, Platform: PlatformSeedance, RateMultiplier: seedanceCostPrice(0.5),
		Extra: map[string]any{AccountModelCostPricingExtraKey: prices}}
}

func TestSeedanceAccountCostUsesModelResolutionAndSelectedAccount(t *testing.T) {
	account := seedanceNativeCostAccount(20,
		ChannelModelPricing{Models: []string{"seedance-2.0"}, BillingMode: BillingModeVideo,
			Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: seedanceCostPrice(0.03)}, {TierLabel: "1080p", PerRequestPrice: seedanceCostPrice(0.05)}}},
		ChannelModelPricing{Models: []string{"bytedance/seedance-2.5"}, BillingMode: BillingModeVideo,
			Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: seedanceCostPrice(0)}}},
	)
	svc := seedanceCostGateway()
	groupID := int64(9)
	for _, tc := range []struct {
		model, resolution string
		accountID         int64
		want              float64
		wantMultiplier    float64
		wantError         bool
	}{
		{"seedance-2.0", "720p", 20, 0.03, 1, false},
		{"seedance-2.0", "1080p", 20, 0.05, 1, false},
		{"Seedance-2.5", "720p", 20, 0, 1, false},
		{"seedance-2.0", "480p", 20, 0, 0, true},
		{"seedance-2.0", "unknown", 20, 0, 0, true},
		{"seedance-2.0-fast", "720p", 20, 0.1, 0.5, false},
		{"seedance-2.0", "720p", 21, 0.1, 0.5, false},
	} {
		t.Run(tc.model+tc.resolution, func(t *testing.T) {
			pending := &SeedanceVideoPendingBilling{GroupID: &groupID, Model: tc.model, Resolution: tc.resolution, TotalCostPerSecond: 0.1}
			selected := account
			if tc.accountID != account.ID {
				selected = &Account{ID: tc.accountID, Platform: PlatformSeedance, RateMultiplier: seedanceCostPrice(0.5)}
			}
			cost, err := svc.resolveSeedanceAccountCost(context.Background(), pending, selected)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, cost.UnitPrice)
			require.Equal(t, tc.wantMultiplier, cost.RateMultiplier)
		})
	}
}

func TestSeedanceAccountCostPreservesUnconfiguredEstimateWithoutGroup(t *testing.T) {
	svc := seedanceCostGateway()
	pending := &SeedanceVideoPendingBilling{Model: "seedance-2.0", Resolution: "720p", TotalCostPerSecond: 0.1}
	account := &Account{ID: 20, Platform: PlatformSeedance, RateMultiplier: seedanceCostPrice(0.5)}
	cost, err := svc.resolveSeedanceAccountCost(context.Background(), pending, account)
	require.NoError(t, err)
	require.Equal(t, &SeedanceAccountCostSnapshot{BillingMode: BillingModeVideo, UnitPrice: 0.1, RateMultiplier: 0.5}, cost)

	account = seedanceNativeCostAccount(20, ChannelModelPricing{Models: []string{"seedance-2.0"},
		BillingMode: BillingModeVideo, PerRequestPrice: seedanceCostPrice(0.03)})
	cost, err = svc.resolveSeedanceAccountCost(context.Background(), pending, account)
	require.NoError(t, err)
	require.Equal(t, 0.03, cost.UnitPrice)
	require.Equal(t, 1.0, cost.RateMultiplier)
}

func TestSeedanceAccountCostFreezesAndFollowsAccountFailover(t *testing.T) {
	svc := seedanceCostGateway()
	groupID := int64(9)
	pending := &SeedanceVideoPendingBilling{StateID: "cost-freeze", UserID: 7, APIKeyID: 8,
		GroupID: &groupID, Model: "seedance-2.5", Resolution: "720p", DurationSeconds: 10,
		TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.2, HoldID: "cost-freeze"}
	require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
	account := seedanceNativeCostAccount(20, ChannelModelPricing{Models: []string{"seedance-2.5"},
		BillingMode: BillingModeVideo, PerRequestPrice: seedanceCostPrice(0.03)})
	require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, "bblabu_v1"))
	require.Equal(t, 0.03, pending.AccountCost.UnitPrice)
	account = seedanceNativeCostAccount(21, ChannelModelPricing{Models: []string{"seedance-2.5"},
		BillingMode: BillingModeVideo, PerRequestPrice: seedanceCostPrice(0.04)})
	require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, "fflink_v1"))
	require.Equal(t, 0.04, pending.AccountCost.UnitPrice)
	prices, ok := account.Extra[AccountModelCostPricingExtraKey].([]ChannelModelPricing)
	require.True(t, ok)
	*prices[0].PerRequestPrice = 50
	*account.RateMultiplier = 9
	repo, ok := svc.seedanceVideoTaskRepo.(*seedanceVideoTaskMemoryRepo)
	require.True(t, ok)
	stored := repo.tasks[pending.StateID]
	require.Equal(t, "fflink_v1", stored.ProviderID)
	require.Equal(t, int64(21), stored.AccountID)
	result, cost, err := BuildSeedanceVideoCompletionBilling(stored, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 10})
	require.NoError(t, err)
	input := &OpenAIRecordUsageInput{Result: result, CostOverride: cost}
	require.NoError(t, ApplySeedanceAccountCost(input, stored))
	require.InDelta(t, 0.4, *input.AccountStatsCostOverride, 1e-12)
	require.Equal(t, 1.0, *input.AccountStatsRateMultiplierOverride)
	require.Equal(t, 2.0, cost.ActualCost)
}

func TestSeedanceAccountCostUsesProviderBillingSecondsAndFixedTaskCost(t *testing.T) {
	for _, tc := range []struct {
		provider string
		mode     BillingMode
		want     float64
	}{{"bblabu_v1", BillingModeVideo, 0.6}, {"fflink_v1", BillingModeVideo, 0.4}, {"bblabu_v1", BillingModePerRequest, 0.04}} {
		t.Run(tc.provider+string(tc.mode), func(t *testing.T) {
			pending := &SeedanceVideoPendingBilling{Model: videoprovider.ModelSeedance25, Resolution: "720p", ProviderID: tc.provider,
				ReferenceVideoCount: 1}
			account := seedanceNativeCostAccount(20, ChannelModelPricing{Models: []string{pending.Model},
				BillingMode: tc.mode, PerRequestPrice: seedanceCostPrice(0.04)})
			var err error
			pending.AccountCost, err = seedanceCostGateway().resolveSeedanceAccountCost(context.Background(), pending, account)
			require.NoError(t, err)
			result, _, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 10, VideoReferenceInputSeconds: 5})
			require.NoError(t, err)
			input := &OpenAIRecordUsageInput{Result: result}
			require.NoError(t, ApplySeedanceAccountCost(input, pending))
			require.InDelta(t, tc.want, *input.AccountStatsCostOverride, 1e-12)
			require.Equal(t, 1.0, *input.AccountStatsRateMultiplierOverride)
		})
	}
	input := &OpenAIRecordUsageInput{}
	require.NoError(t, ApplySeedanceAccountCost(input, &SeedanceVideoPendingBilling{}))
	require.Nil(t, input.AccountStatsCostOverride)
}

func TestSeedanceNativeAccountCostValidation(t *testing.T) {
	for _, tc := range []struct {
		name, model, tier string
		mode              BillingMode
		price             *float64
		wantError         bool
	}{
		{"free", "Seedance-2.5", "720p", BillingModeVideo, seedanceCostPrice(0), false},
		{"4k", "seedance-2.0", "4K", BillingModeVideo, seedanceCostPrice(0.1), false},
		{"unsupported", "seedance-2.5", "1080p", BillingModeVideo, seedanceCostPrice(0.1), true},
		{"unknown", "seedance-2.0", "8k", BillingModeVideo, seedanceCostPrice(0.1), true},
		{"missing", "seedance-2.0", "720p", BillingModeVideo, nil, true},
		{"negative", "seedance-2.0", "720p", BillingModeVideo, seedanceCostPrice(-1), true},
		{"nan", "seedance-2.0", "720p", BillingModeVideo, seedanceCostPrice(math.NaN()), true},
		{"token", "seedance-2.0", "720p", BillingModeToken, seedanceCostPrice(0.1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{{
				Models: []string{tc.model}, BillingMode: tc.mode, Intervals: []PricingInterval{{TierLabel: tc.tier, PerRequestPrice: tc.price}}}}}
			err := NormalizeAccountModelCostPricingExtra(PlatformSeedance, extra)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	alias := ChannelModelPricing{Platform: PlatformSeedance, Models: []string{"bytedance/seedance-2.5", "Seedance-2.5"}, BillingMode: BillingModeVideo, PerRequestPrice: seedanceCostPrice(0.1)}
	require.Error(t, NormalizeAccountModelCostPricingExtra(PlatformSeedance, map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{alias}}))
	duplicate := ChannelModelPricing{Platform: PlatformSeedance, Models: []string{"seedance-2.0"}, BillingMode: BillingModeVideo,
		Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: seedanceCostPrice(0.1)}, {TierLabel: "720P", PerRequestPrice: seedanceCostPrice(0.2)}}}
	require.Error(t, NormalizeAccountModelCostPricingExtra(PlatformSeedance, map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{duplicate}}))
}

type seedanceCostUsageRepo struct {
	UsageLogRepository
	logs []*UsageLog
}

func (r *seedanceCostUsageRepo) Create(_ context.Context, usage *UsageLog) (bool, error) {
	r.logs = append(r.logs, usage)
	return true, nil
}

func TestRecordSeedanceVideoUsagePreservesHistoricalCostSnapshotMultiplier(t *testing.T) {
	account := seedanceVideoTestAccount()
	account.RateMultiplier = seedanceCostPrice(9)
	account.Extra = map[string]any{"quota_limit": float64(1000)}
	user := &User{ID: 7}
	apiKey := &APIKey{ID: 8, User: user, UserID: 7}
	billing := &seedanceVideoBillingRepo{}
	logs := &seedanceCostUsageRepo{}
	svc := &OpenAIGatewayService{usageBillingRepo: billing, usageLogRepo: logs, cfg: &config.Config{}, deferredService: &DeferredService{}}
	pending := &SeedanceVideoPendingBilling{TaskID: "http-cost", Model: "seedance-2.0", Resolution: "720p", ProviderID: "fflink_v1",
		TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.2,
		AccountCost: &SeedanceAccountCostSnapshot{BillingMode: BillingModeVideo, UnitPrice: 0.04, RateMultiplier: 0.5}}
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 10})
	require.NoError(t, err)
	require.NoError(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
		Result: result, CostOverride: cost, APIKey: apiKey, User: user, Account: account,
	}))
	require.Len(t, logs.logs, 1)
	require.InDelta(t, 0.4, *logs.logs[0].AccountStatsCost, 1e-12)
	require.Equal(t, 0.5, *logs.logs[0].AccountRateMultiplier)
	require.InDelta(t, 0.2, *billing.commands[0].SalesCost, 1e-12)
	require.Equal(t, 2.0, billing.commands[0].BalanceCost)
	require.Equal(t, 9.0, billing.commands[0].AccountQuotaCost)
}

func TestRecordSeedanceVideoUsageNativeCostDoesNotMultiplyAccountRate(t *testing.T) {
	account := seedanceVideoTestAccount()
	account.RateMultiplier = seedanceCostPrice(9)
	account.Extra = map[string]any{"quota_limit": float64(1000), AccountModelCostPricingExtraKey: []ChannelModelPricing{{
		Models: []string{"seedance-2.0"}, BillingMode: BillingModeVideo, PerRequestPrice: seedanceCostPrice(0.04),
	}}}
	user := &User{ID: 7}
	apiKey := &APIKey{ID: 8, User: user, UserID: 7}
	billing := &seedanceVideoBillingRepo{}
	logs := &seedanceCostUsageRepo{}
	svc := &OpenAIGatewayService{usageBillingRepo: billing, usageLogRepo: logs, cfg: &config.Config{}, deferredService: &DeferredService{}}
	pending := &SeedanceVideoPendingBilling{TaskID: "native-http-cost", Model: "seedance-2.0", Resolution: "720p", ProviderID: "fflink_v1",
		TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.2}
	var err error
	pending.AccountCost, err = svc.resolveSeedanceAccountCost(context.Background(), pending, account)
	require.NoError(t, err)
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 10})
	require.NoError(t, err)
	require.NoError(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
		Result: result, CostOverride: cost, APIKey: apiKey, User: user, Account: account,
	}))
	require.Len(t, logs.logs, 1)
	require.InDelta(t, 0.4, *logs.logs[0].AccountStatsCost, 1e-12)
	require.Equal(t, 1.0, *logs.logs[0].AccountRateMultiplier)
	require.InDelta(t, 0.4, *billing.commands[0].SalesCost, 1e-12)
	require.Equal(t, 2.0, billing.commands[0].BalanceCost)
	require.Equal(t, 9.0, billing.commands[0].AccountQuotaCost)
}
