package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func videoReferenceMeteringCapability(provider videoprovider.ID) videoprovider.VideoModelCapability {
	capability := videoProtocolCapability(provider)
	capability.MaxReferenceVideoSeconds, capability.MaxCombinedVideoSeconds = 30, 45
	return capability
}

func videoReferenceMeteringPrices(combinedSide string) (ChannelModelPricing, ChannelModelPricing) {
	sales := explicitVideoPrice("studio-video", VideoBillingPerSecond, 0.1)
	purchase := explicitVideoPrice("studio-video", VideoBillingPerSecond, 0.04)
	if combinedSide == "sales" {
		sales.VideoQuantityPolicy = VideoQuantityOutputAndReferenceSeconds
	}
	if combinedSide == "purchase" {
		purchase.VideoQuantityPolicy = VideoQuantityOutputAndReferenceSeconds
	}
	return sales, purchase
}

func TestVideoReferenceMeteringAssignmentRejectsUnsupportedSalesAndPurchase(t *testing.T) {
	for _, side := range []string{"sales", "purchase"} {
		for _, withReference := range []bool{false, true} {
			t.Run(side+map[bool]string{false: "/without_reference", true: "/with_reference"}[withReference], func(t *testing.T) {
				capability := videoReferenceMeteringCapability(videoprovider.ProviderZhiqiV1)
				sales, purchase := videoReferenceMeteringPrices(side)
				account := videoProtocolAccount(20, videoprovider.ProviderZhiqiV1, capability)
				account.Extra[AccountModelCostPricingExtraKey] = []ChannelModelPricing{purchase}
				svc, key := videoTasksGatewayPricingService(sales)
				repo := &videoTasksGatewayRepo{}
				svc.seedanceVideoTaskRepo = repo
				references := 0
				if withReference {
					references = 1
				}
				quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, SeedanceVideoRequestInfo{
					Model: capability.Model, Resolution: "720p", DurationSeconds: 6, ReferenceVideoCount: references, Capability: &capability})
				require.NoError(t, err)
				pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff",
					UserID: 7, APIKeyID: 8, HoldID: "hold", Model: capability.Model, Resolution: "720p", DurationSeconds: 6,
					ReferenceVideoCount: references, SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount}
				require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
				err = svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, string(videoprovider.ProviderZhiqiV1))
				if !withReference {
					require.NoError(t, err, "an output-only request does not require reference usage")
					return
				}
				if side == "sales" {
					require.ErrorIs(t, err, ErrVideoTaskSalesCapabilityMismatch)
				} else {
					require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
				}
				require.Zero(t, pending.AccountID)
				require.Zero(t, repo.tasks[pending.StateID].AccountID)
				require.Nil(t, pending.ExecutionSnapshot)
			})
		}
	}
}

func TestVideoReferenceMeteringAdmissionSkipsZhiqiWhenJSONSupplierCanMeter(t *testing.T) {
	for _, side := range []string{"sales", "purchase"} {
		t.Run(side, func(t *testing.T) {
			sales, purchase := videoReferenceMeteringPrices(side)
			accounts := []Account{*videoProtocolAccount(20, videoprovider.ProviderZhiqiV1, videoReferenceMeteringCapability(videoprovider.ProviderZhiqiV1)),
				*videoProtocolAccount(21, videoprovider.ProviderVideoTasksJSONV1, videoReferenceMeteringCapability(videoprovider.ProviderVideoTasksJSONV1))}
			for i := range accounts {
				accounts[i].Extra[AccountModelCostPricingExtraKey] = []ChannelModelPricing{purchase}
			}
			svc, key := videoTasksGatewayPricingService(sales)
			svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
			upstream := &seedanceVideoUpstream{}
			svc.httpUpstream = upstream
			body := []byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p","referenceVideos":["https://assets.example/reference.mp4"]}`)
			request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, body)
			require.NoError(t, err)
			require.Equal(t, videoprovider.ProfileBBLabuOfficial, info.Capability.Profile)
			require.Len(t, request.ReferenceVideos, 1)
			svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts[:1]}
			request, _, _, err = svc.PrepareVideoRequestForKey(context.Background(), key, body)
			require.Nil(t, request)
			if side == "purchase" {
				require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
			} else {
				require.ErrorContains(t, err, "reference video metering")
			}
			require.Empty(t, upstream.requests, "incompatible metering must be rejected before any submission")
			request, _, _, err = svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p"}`))
			require.NoError(t, err)
			require.Empty(t, request.ReferenceVideos)
		})
	}
}

func TestVideoReferenceMeteringCatalogHidesOnlyUnsupportedReferenceContracts(t *testing.T) {
	for _, side := range []string{"sales", "purchase", "neither"} {
		t.Run(side, func(t *testing.T) {
			capability := videoReferenceMeteringCapability(videoprovider.ProviderZhiqiV1)
			sales, purchase := videoReferenceMeteringPrices(side)
			account := videoProtocolAccount(20, videoprovider.ProviderZhiqiV1, capability)
			account.Extra[AccountModelCostPricingExtraKey] = []ChannelModelPricing{purchase}
			svc, key := videoTasksGatewayPricingService(sales)
			svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
			models, err := svc.ListVideoMediaModels(context.Background(), key)
			require.NoError(t, err)
			require.Len(t, models, 1)
			require.Len(t, models[0].Variants, 1)
			variant := models[0].Variants[0]
			if side == "neither" {
				require.Equal(t, capability.MaxVideos, variant.MaxVideos, "output-second pricing can still accept reference videos")
				require.Equal(t, capability.MaxReferenceVideoSeconds, variant.MaxReferenceVideoSeconds)
			} else {
				require.Zero(t, variant.MaxVideos)
				require.Zero(t, variant.MaxReferenceVideoSeconds)
				require.Zero(t, variant.MaxCombinedVideoSeconds)
				require.False(t, variant.RequireReferenceVideoDurations)
			}
			require.Len(t, variant.Prices, 2)
			require.Equal(t, capability.MaxImages, variant.MaxImages)
			stored, ok := account.VideoModelCapability(capability.Model)
			require.True(t, ok)
			require.Equal(t, capability.MaxVideos, stored.MaxVideos, "catalog filtering must not edit account capabilities")
		})
	}
}
