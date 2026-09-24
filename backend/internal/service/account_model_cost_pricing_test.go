//go:build unit

package service

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func modelCostAccount(platform string, entries ...ChannelModelPricing) *Account {
	return &Account{ID: 7, Platform: platform, Type: AccountTypeAPIKey,
		RateMultiplier: testPtrFloat64(0.25),
		Extra:          map[string]any{AccountModelCostPricingExtraKey: entries}}
}

func TestAccountModelCostValidation(t *testing.T) {
	valid := ChannelModelPricing{Models: []string{"custom"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0)}
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"null", nil}, {"object", map[string]any{}},
		{"nan", []ChannelModelPricing{{Models: []string{"custom"}, InputPrice: testPtrFloat64(math.NaN())}}},
		{"negative", []ChannelModelPricing{{Models: []string{"custom"}, InputPrice: testPtrFloat64(-1)}}},
		{"mode", []ChannelModelPricing{{Models: []string{"custom"}, BillingMode: "unknown"}}},
		{"empty_model", []ChannelModelPricing{{Models: []string{" "}, InputPrice: testPtrFloat64(1)}}},
		{"duplicate", []ChannelModelPricing{valid, valid}},
		{"wrong_platform", []ChannelModelPricing{{Platform: PlatformGemini, Models: []string{"custom"}, InputPrice: testPtrFloat64(1)}}},
		{"missing_price", []ChannelModelPricing{{Models: []string{"custom"}, BillingMode: BillingModeToken}}},
		{"missing_tier_price", []ChannelModelPricing{{Models: []string{"custom"}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0),
			Intervals: []PricingInterval{{TierLabel: "1K"}}}}},
		{"duplicate_tier", []ChannelModelPricing{{Models: []string{"custom"}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0),
			Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0)}, {TierLabel: "1k", PerRequestPrice: testPtrFloat64(1)}}}}},
		{"invalid_video_resolution", []ChannelModelPricing{{Models: []string{"custom"}, BillingMode: BillingModeVideo, PerRequestPrice: testPtrFloat64(0),
			Intervals: []PricingInterval{{TierLabel: "bad", PerRequestPrice: testPtrFloat64(1)}}}}},
		{"interval_overlap", []ChannelModelPricing{{Models: []string{"custom"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0),
			Intervals: []PricingInterval{{MinTokens: 0, InputPrice: testPtrFloat64(1)}, {MinTokens: 2, InputPrice: testPtrFloat64(2)}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := NormalizeAccountModelCostPricingExtra(PlatformOpenAI, map[string]any{AccountModelCostPricingExtraKey: tc.raw})
			require.Error(t, err)
		})
	}
	extra := map[string]any{AccountModelCostPricingExtraKey: []any{}}
	require.NoError(t, NormalizeAccountModelCostPricingExtra(PlatformOpenAI, extra))
	require.Empty(t, extra[AccountModelCostPricingExtraKey])
	require.NoError(t, NormalizeAccountModelCostPricingExtra(PlatformOpenAI, nil))
}

func TestAccountModelCostReasoningUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name        string
		multipliers map[string]float64
		effort      string
		want        float64
	}{
		{"legacy max", nil, "max", 3},
		{"legacy other effort", nil, "high", 1},
		{"new map wins", map[string]float64{"max": 2, "high": 1.5}, "max", 2},
		{"new high effort", map[string]float64{"high": 1.5}, "high", 1.5},
		{"cleared map stays cleared", map[string]float64{}, "max", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price := map[string]any{"models": []string{"vendor-model"}, "billing_mode": "token",
				"input_price": 0.01, "max_reasoning_effort_multiplier": 3.0}
			if tc.multipliers != nil {
				price["reasoning_effort_multipliers"] = tc.multipliers
			}
			account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
				AccountModelCostPricingExtraKey: []any{price},
			}}
			input := CostInput{Model: "vendor-model", ReasoningEffort: tc.effort, Tokens: UsageTokens{InputTokens: 100}}
			cost, err := ResolveAccountModelCost(context.Background(), newTestBillingService(), account, input)
			require.NoError(t, err)
			require.InDelta(t, tc.want, *cost, 1e-12)
			require.NoError(t, NormalizeAccountModelCostPricingExtra(account.Platform, account.Extra))
			cost, err = ResolveAccountModelCost(context.Background(), newTestBillingService(), account, input)
			require.NoError(t, err)
			require.InDelta(t, tc.want, *cost, 1e-12, "saving must preserve the purchase price")
		})
	}
	for _, multiplier := range []float64{0, -1} {
		err := NormalizeAccountModelCostPricingExtra(PlatformOpenAI, map[string]any{
			AccountModelCostPricingExtraKey: []any{map[string]any{
				"models": []string{"vendor-model"}, "input_price": 0.01, "max_reasoning_effort_multiplier": multiplier,
			}},
		})
		require.Error(t, err)
	}
}

func TestAccountModelCostReasoningAcrossBillingModes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        BillingMode
		unitPrice   float64
		multipliers map[string]float64
		input       CostInput
		want        float64
		wantError   bool
	}{
		{"token once", BillingModeToken, 0.01, map[string]float64{"high": 3}, CostInput{ReasoningEffort: "high", Tokens: UsageTokens{InputTokens: 100}}, 3, false},
		{"per request", BillingModePerRequest, 0.4, map[string]float64{"high": 3}, CostInput{ReasoningEffort: "high", RequestCount: 1}, 1.2, false},
		{"image count", BillingModeImage, 0.4, map[string]float64{"high": 3}, CostInput{ReasoningEffort: "high", RequestCount: 2}, 2.4, false},
		{"video seconds", BillingModeVideo, 0.4, map[string]float64{"high": 3}, CostInput{ReasoningEffort: "high", UsageUnits: 5}, 6, false},
		{"no configured multiplier", BillingModePerRequest, 0.4, nil, CostInput{ReasoningEffort: "high"}, 0.4, false},
		{"other effort", BillingModePerRequest, 0.4, map[string]float64{"high": 3}, CostInput{ReasoningEffort: "low"}, 0.4, false},
		{"no effort", BillingModePerRequest, 0.4, map[string]float64{"high": 3}, CostInput{}, 0.4, false},
		{"free price", BillingModePerRequest, 0, map[string]float64{"high": 3}, CostInput{ReasoningEffort: "high"}, 0, false},
		{"multiplied cost overflow", BillingModePerRequest, math.MaxFloat64 / 2, map[string]float64{"high": 3}, CostInput{ReasoningEffort: "high"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := ChannelModelPricing{Models: []string{"vendor-model"}, BillingMode: tc.mode,
				ReasoningEffortMultipliers: tc.multipliers}
			if tc.mode == BillingModeToken {
				pricing.InputPrice = testPtrFloat64(tc.unitPrice)
			} else {
				pricing.PerRequestPrice = testPtrFloat64(tc.unitPrice)
			}
			account := modelCostAccount(PlatformOpenAI, pricing)
			require.NoError(t, NormalizeAccountModelCostPricingExtra(account.Platform, account.Extra))
			input := tc.input
			input.Model = "vendor-model"
			cost, err := ResolveAccountModelCost(context.Background(), newTestBillingService(), account, input)
			if tc.wantError {
				require.ErrorContains(t, err, "exceeds the supported range")
				require.Nil(t, cost)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cost)
			require.InDelta(t, tc.want, *cost, 1e-12)
		})
	}
}

func TestAccountModelCostTokenUsesOnlyPurchasePrices(t *testing.T) {
	account := modelCostAccount(PlatformAnthropic, ChannelModelPricing{
		Models: []string{"claude-fable-5-1"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(0.001), OutputPrice: testPtrFloat64(0.002),
		CacheReadPrice: testPtrFloat64(0.0001), CacheWritePrice: testPtrFloat64(0.003),
		CacheWrite1hPrice: testPtrFloat64(0.005),
	})
	input := CostInput{Model: "CLAUDE-FABLE-5-1", RateMultiplier: 999,
		ServiceTier: "flex", ReasoningEffort: "max",
		Group: &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"claude-fable-5-1"}, InputPrice: testPtrFloat64(100)}}},
		Tokens: UsageTokens{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 20,
			CacheCreationTokens: 10, CacheCreation5mTokens: 4, CacheCreation1hTokens: 6}}
	cost, err := ResolveAccountModelCost(context.Background(), newTestBillingService(), account, input)
	require.NoError(t, err)
	require.InDelta(t, 0.244, *cost, 1e-12)
	require.Equal(t, 0.25, account.BillingRateMultiplier())
	input.Model = "claude-sonnet-4"
	cost, err = ResolveAccountModelCost(context.Background(), newTestBillingService(), account, input)
	require.NoError(t, err)
	require.Nil(t, cost)
}

func TestAccountModelCostIntervalsUseInputContextAndRespectZero(t *testing.T) {
	account := modelCostAccount(PlatformOpenAI, ChannelModelPricing{Models: []string{"gpt-5.4"},
		BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0.01), OutputPrice: testPtrFloat64(0.02),
		ImageInputPrice: testPtrFloat64(0),
		Intervals:       []PricingInterval{{MinTokens: 100, InputPrice: testPtrFloat64(0.03)}}})
	for _, tc := range []struct {
		name   string
		tokens UsageTokens
		want   float64
	}{
		{"output_does_not_select_high_tier", UsageTokens{InputTokens: 50, OutputTokens: 200}, 4.5},
		{"cache_selects_high_tier_without_default_cache_price", UsageTokens{InputTokens: 50, CacheReadTokens: 60}, 1.5},
		{"explicit_zero_image_input", UsageTokens{InputTokens: 50, ImageInputTokens: 20}, 0.3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := ResolveAccountModelCost(context.Background(), newTestBillingService(), account, CostInput{Model: "gpt-5.4", Tokens: tc.tokens})
			require.NoError(t, err)
			require.InDelta(t, tc.want, *cost, 1e-12)
		})
	}
}

func TestAccountModelCostMediaAndMissingTier(t *testing.T) {
	account := modelCostAccount(PlatformSeedance, ChannelModelPricing{Models: []string{"seedance-2.0"},
		BillingMode: BillingModeVideo, PerRequestPrice: testPtrFloat64(0.03),
		Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: testPtrFloat64(0)}}})
	for _, tc := range []struct {
		tier string
		want float64
	}{{"720P", 0}, {"1080p", 0.3}} {
		cost, err := ResolveAccountModelCost(context.Background(), nil, account,
			CostInput{Model: "seedance-2.0", SizeTier: tc.tier, UsageUnits: 10, RateMultiplier: 8})
		require.NoError(t, err)
		require.InDelta(t, tc.want, *cost, 1e-12)
	}
	_, err := ResolveAccountModelCost(context.Background(), nil, account, CostInput{Model: "seedance-2.0"})
	require.Error(t, err)
	account = modelCostAccount(PlatformOpenAI, ChannelModelPricing{Models: []string{"image-model"}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.5),
		Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0)}, {TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.2)}}})
	cost, err := resolveAccountModelCostWithImages(context.Background(), nil, account, CostInput{Model: "image-model"}, 3, map[string]int{"1K": 1, "2K": 2})
	require.NoError(t, err)
	require.InDelta(t, 0.4, *cost, 1e-12)
	cost, err = ResolveAccountModelCost(context.Background(), nil, account, CostInput{Model: "image-model", SizeTier: "4K"})
	require.NoError(t, err)
	require.Equal(t, 0.5, *cost)
	account = modelCostAccount(PlatformSeedance, ChannelModelPricing{Models: []string{"seedance-2.0"}, BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(0.4)})
	cost, err = ResolveAccountModelCost(context.Background(), nil, account, CostInput{Model: "seedance-2.0", UsageUnits: 120, RequestCount: 1})
	require.NoError(t, err)
	require.Equal(t, 0.4, *cost)
}

func TestAccountModelCostImageTiersApplyReasoningOnce(t *testing.T) {
	account := modelCostAccount(PlatformOpenAI, ChannelModelPricing{Models: []string{"image-model"},
		BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.5),
		ReasoningEffortMultipliers: map[string]float64{"high": 3},
		Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.2)}}})
	for _, tc := range []struct {
		name  string
		sizes map[string]int
		want  float64
	}{
		{"no breakdown", nil, 4.5},
		{"complete breakdown", map[string]int{"1K": 1, "2K": 2}, 1.2},
		{"remaining use default", map[string]int{"1K": 1, "2K": 1}, 2.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := resolveAccountModelCostWithImages(context.Background(), nil, account,
				CostInput{Model: "image-model", SizeTier: "4K", ReasoningEffort: "high"}, 3, tc.sizes)
			require.NoError(t, err)
			require.NotNil(t, cost)
			require.InDelta(t, tc.want, *cost, 1e-12)
		})
	}
}

func TestAccountModelCostRequiresDefaultsBeforeSaving(t *testing.T) {
	for _, mode := range []BillingMode{BillingModeToken, BillingModeImage, BillingModeVideo, BillingModePerRequest} {
		t.Run(string(mode), func(t *testing.T) {
			entry := ChannelModelPricing{Models: []string{"custom"}, BillingMode: mode,
				Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: testPtrFloat64(0.2)}}}
			if mode == BillingModeToken {
				entry.Intervals = []PricingInterval{{MinTokens: 100, InputPrice: testPtrFloat64(0.01)}}
			}
			extra := map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{entry}}
			require.Error(t, NormalizeAccountModelCostPricingExtra(PlatformOpenAI, extra))
			if mode == BillingModeToken {
				entry.InputPrice = testPtrFloat64(0)
			} else {
				entry.PerRequestPrice = testPtrFloat64(0)
			}
			account := modelCostAccount(PlatformOpenAI, entry)
			require.NoError(t, NormalizeAccountModelCostPricingExtra(account.Platform, account.Extra))
			cost, err := ResolveAccountModelCost(context.Background(), newTestBillingService(), account,
				CostInput{Model: "custom", SizeTier: "1080p", UsageUnits: 10, Tokens: UsageTokens{InputTokens: 50}})
			require.NoError(t, err)
			require.Zero(t, *cost, "an explicit free default covers missing contexts and tiers")
		})
	}
}

func TestAccountModelCostIncompleteImageSizesUseResolvedTier(t *testing.T) {
	account := modelCostAccount(PlatformOpenAI, ChannelModelPricing{Models: []string{"image-model"},
		BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.5),
		Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0)}, {TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.2)}}})
	for _, tc := range []struct {
		name         string
		resolvedTier string
		sizes        map[string]int
		want         float64
		wantError    bool
	}{
		{"remaining_use_free_resolved_tier", "1K", map[string]int{"2K": 1}, 0.2, false},
		{"remaining_use_default_price", "4K", map[string]int{"2K": 1}, 1.2, false},
		{"explicit_free_known_tier", "4K", map[string]int{"1K": 1}, 1, false},
		{"too_many_known_images", "1K", map[string]int{"2K": 4}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := resolveAccountModelCostWithImages(context.Background(), nil, account,
				CostInput{Model: "image-model", SizeTier: tc.resolvedTier}, 3, tc.sizes)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.InDelta(t, tc.want, *cost, 1e-12)
		})
	}
}

func TestGatewayRecordUsageAccountModelCostPreservesQuotas(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	account := modelCostAccount(PlatformAnthropic, ChannelModelPricing{Models: []string{"purchase-model"}, InputPrice: testPtrFloat64(0.001)})
	account.Extra["quota_limit"] = 100.0
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{RequestID: "account-native-cost", Model: "claude-sonnet-4", UpstreamModel: "purchase-model",
			Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 10}},
		APIKey: &APIKey{ID: 501, Quota: 100}, User: &User{ID: 601}, Account: account,
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 0.1, *usageRepo.lastLog.AccountStatsCost, 1e-12)
	require.Equal(t, 1.0, *usageRepo.lastLog.AccountRateMultiplier)
	require.InDelta(t, 0.1, *billingRepo.lastCmd.SalesCost, 1e-12)
	require.InDelta(t, usageRepo.lastLog.TotalCost*0.25, billingRepo.lastCmd.AccountQuotaCost, 1e-8)
	require.InDelta(t, usageRepo.lastLog.ActualCost, billingRepo.lastCmd.BalanceCost, 1e-8)
}
