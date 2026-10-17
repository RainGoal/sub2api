package service

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func explicitVideoPrice(model string, unit VideoBillingUnit, price float64) ChannelModelPricing {
	return ChannelModelPricing{Platform: PlatformSeedance, Models: []string{model}, BillingMode: BillingModeVideo,
		VideoPricingVersion: VideoPricingVersion, VideoBillingUnit: unit, VideoQuantityPolicy: VideoQuantityOutputSeconds,
		PerRequestPrice: &price}
}

func TestVideoTasksIndependentSalesAndPurchaseUnits(t *testing.T) {
	for _, salesUnit := range []VideoBillingUnit{VideoBillingPerSecond, VideoBillingPerVideo} {
		for _, purchaseUnit := range []VideoBillingUnit{VideoBillingPerSecond, VideoBillingPerVideo} {
			t.Run(string(salesUnit)+"/"+string(purchaseUnit), func(t *testing.T) {
				salesPrice, expectedSales := 0.1, 2.0
				if salesUnit == VideoBillingPerVideo {
					salesPrice, expectedSales = 0.7, 1.4
				}
				purchasePrice, expectedPurchase := 0.04, 0.4
				if purchaseUnit == VideoBillingPerVideo {
					purchasePrice, expectedPurchase = 0.3, 0.3
				}
				row := explicitVideoPrice("custom-video", salesUnit, salesPrice)
				channels := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{row}})
				price, err := ResolveVideoSalesPrice(context.Background(), channels, seedanceSalesTestGroup(), "custom-video", "")
				require.NoError(t, err)
				sales, err := NewVideoTaskSalesSnapshot(price, 2, VideoQuantityOutputSeconds, 0)
				require.NoError(t, err)
				cost, err := sales.Cost(10, 0)
				require.NoError(t, err)
				require.InDelta(t, expectedSales, cost.ActualCost, 1e-12)
				account := seedanceNativeCostAccount(1, explicitVideoPrice("custom-video", purchaseUnit, purchasePrice))
				purchase, err := ResolveVideoTaskAccountCost(account, "custom-video", "", true, VideoQuantityOutputSeconds, 0)
				require.NoError(t, err)
				actualPurchase, err := purchase.Cost(10, 0)
				require.NoError(t, err)
				require.InDelta(t, expectedPurchase, actualPurchase, 1e-12)
				require.Equal(t, 1.0, purchase.RateMultiplier)
			})
		}
	}
}

func TestVideoTasksExactReferenceSecondsAndIndependentPolicies(t *testing.T) {
	sales := &VideoTaskSalesSnapshot{Version: 1, BillingUnit: VideoBillingPerSecond, UnitPrice: 0.1,
		RateMultiplier: 2, QuantityPolicy: VideoQuantityOutputSeconds, MaxBillableSeconds: 10}
	cost, err := sales.Cost(8.5, 2.75)
	require.NoError(t, err)
	require.InDelta(t, 1.7, cost.ActualCost, 1e-12)
	purchase := &SeedanceAccountCostSnapshot{Version: 1, BillingMode: BillingModeVideo, BillingUnit: VideoBillingPerSecond,
		UnitPrice: 0.04, RateMultiplier: 1, QuantityPolicy: VideoQuantityOutputAndReferenceSeconds, MaxBillableSeconds: 12}
	actualPurchase, err := purchase.Cost(8.5, 2.75)
	require.NoError(t, err)
	require.InDelta(t, 0.45, actualPurchase, 1e-12)
	_, err = purchase.Cost(10, 2.75)
	require.ErrorContains(t, err, "configured limit")
	purchase.QuantityPolicy = VideoQuantityRequestedSeconds
	input := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{VideoDurationSecondsExact: 8.5}}
	require.NoError(t, ApplySeedanceAccountCost(input, &SeedanceVideoPendingBilling{DurationSeconds: 6, AccountCost: purchase}))
	require.InDelta(t, 0.24, *input.AccountStatsCostOverride, 1e-12)
}

func TestVideoTasksVersionAndLegacyPriceCompatibility(t *testing.T) {
	group := seedanceSalesTestGroup()
	legacy := ChannelModelPricing{Platform: PlatformSeedance, Models: []string{"seedance-2.0"}, BillingMode: BillingModePerRequest,
		PerRequestPrice: seedanceCostPrice(9)}
	channels := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{legacy}})
	price, err := ResolveVideoSalesPrice(context.Background(), channels, group, "seedance-2.0", "720p")
	require.NoError(t, err)
	require.Equal(t, VideoBillingPerSecond, price.BillingUnit)
	require.Equal(t, 0.1, price.UnitPrice)
	row := explicitVideoPrice("seedance-2.0", VideoBillingPerVideo, 0.7)
	channels = seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{row}})
	projected, err := ProjectSeedanceSalesPrices(context.Background(), channels, group)
	require.NoError(t, err)
	require.Empty(t, projected.VideoModelPrices)
	legacyPrice, _, err := ResolveSeedanceSalesPrice(context.Background(), channels, group, "seedance-2.0", "720p")
	require.NoError(t, err)
	require.Nil(t, legacyPrice)
	account := seedanceNativeCostAccount(1, legacy)
	purchase, err := ResolveVideoTaskAccountCost(account, "seedance-2.0", "", true, "per_video", 0)
	require.NoError(t, err)
	require.Equal(t, VideoBillingPerVideo, purchase.BillingUnit)
	require.Empty(t, purchase.QuantityPolicy)
	_, err = ResolveVideoTaskAccountCost(&Account{Platform: PlatformSeedance}, "custom", "", true, VideoQuantityOutputSeconds, 0)
	require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
}

func TestVideoTasksExplicitAliasesDoNotActivateLegacyAliasFallback(t *testing.T) {
	row := explicitVideoPrice("Seedance-2.0", VideoBillingPerVideo, 0.7)
	account := seedanceNativeCostAccount(1, row)
	price, err := LookupAccountModelCostPricing(account, "seedance-2.0")
	require.NoError(t, err)
	require.Nil(t, price)
	price, err = LookupAccountModelCostPricing(account, "Seedance-2.0")
	require.NoError(t, err)
	require.NotNil(t, price)
	channels := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{row}})
	sales, err := ResolveVideoSalesPrice(context.Background(), channels, seedanceSalesTestGroup(), "seedance-2.0", "720p")
	require.NoError(t, err)
	require.Nil(t, sales)
}

func TestVideoTasksVersionedResolutionPrices(t *testing.T) {
	row := explicitVideoPrice("wan-2.6-exact", VideoBillingPerVideo, 0.7)
	row.PerRequestPrice = nil
	row.Intervals = []PricingInterval{{TierLabel: "768P", PerRequestPrice: seedanceCostPrice(0.5)}, {TierLabel: "2k", PerRequestPrice: seedanceCostPrice(0.8)}}
	require.NoError(t, normalizeSeedanceSalesPricing([]ChannelModelPricing{row}))
	channels := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{row}})
	price, err := ResolveVideoSalesPrice(context.Background(), channels, seedanceSalesTestGroup(), "wan-2.6-exact", "768p")
	require.NoError(t, err)
	require.Equal(t, 0.5, price.UnitPrice)
	price, err = ResolveVideoSalesPrice(context.Background(), channels, seedanceSalesTestGroup(), "wan-2.6-exact", "4k")
	require.NoError(t, err)
	require.Nil(t, price)
	_, err = ResolveVideoSalesPrice(context.Background(), channels, &Group{Platform: PlatformGrok}, "wan-2.6-exact", "768p")
	require.Error(t, err)
}

func TestVideoTasksInvalidPricingFailsClosed(t *testing.T) {
	for _, mutate := range []func(*ChannelModelPricing){
		func(p *ChannelModelPricing) { p.VideoPricingVersion = 2 },
		func(p *ChannelModelPricing) { p.VideoPricingVersion = 0 },
		func(p *ChannelModelPricing) { p.BillingMode = BillingModePerRequest },
		func(p *ChannelModelPricing) { p.VideoBillingUnit = "unknown" },
		func(p *ChannelModelPricing) { p.VideoMaxBillableSeconds = math.Inf(1) },
		func(p *ChannelModelPricing) { p.Models = []string{"custom-*"} },
	} {
		row := explicitVideoPrice("custom", VideoBillingPerVideo, 0)
		mutate(&row)
		require.Error(t, normalizeSeedanceSalesPricing([]ChannelModelPricing{row}))
	}
	zero := explicitVideoPrice("custom", VideoBillingPerVideo, 0)
	zero.VideoQuantityPolicy = ""
	require.NoError(t, normalizeSeedanceSalesPricing([]ChannelModelPricing{zero}))
	_, err := VideoBillingQuantity(VideoBillingPerSecond, VideoQuantityOutputSeconds, 0, math.NaN(), 0)
	require.Error(t, err)
}
