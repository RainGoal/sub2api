package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestSeedanceSalesUpstreamRestrictionAcceptsProviderAlias(t *testing.T) {
	ctx := context.Background()
	group := seedanceSalesTestGroup()
	channels := seedanceSalesTestService(&Channel{
		Status: StatusActive, RestrictModels: true, BillingModelSource: BillingModelSourceUpstream,
		ModelPricing: []ChannelModelPricing{{Platform: PlatformSeedance, Models: []string{videoprovider.ModelSeedance25},
			BillingMode: BillingModeVideo, PerRequestPrice: seedanceSalesTestPrice(0.2)}},
	})
	account := &Account{Platform: PlatformSeedance, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test", "video_provider": "fflink_v1",
		"model_mapping": map[string]any{videoprovider.ModelSeedance25: "bytedance/seedance-2.5"},
	}}
	require.NoError(t, validateSeedanceAccountCredentials(account.Platform, account.Type, account.Credentials))
	price, _, err := ResolveSeedanceSalesPrice(ctx, channels, group, videoprovider.ModelSeedance25, "720p")
	require.NoError(t, err)
	require.NotNil(t, price)
	gateway := &OpenAIGatewayService{channelService: channels}
	require.True(t, gateway.needsUpstreamChannelRestrictionCheck(ctx, &group.ID))
	require.False(t, gateway.isUpstreamModelRestrictedByChannel(ctx, group.ID, account, videoprovider.ModelSeedance25, false),
		"a valid provider alias must match the same canonical Seedance sales entry")
}

func TestSeedanceSalesUpstreamRestrictionKeepsUnpricedModelsBlocked(t *testing.T) {
	ctx := context.Background()
	group := seedanceSalesTestGroup()
	channels := seedanceSalesTestService(&Channel{
		Status: StatusActive, RestrictModels: true, BillingModelSource: BillingModelSourceUpstream,
		ModelPricing: []ChannelModelPricing{{Platform: PlatformSeedance, Models: []string{videoprovider.ModelSeedance25},
			BillingMode: BillingModeVideo, PerRequestPrice: seedanceSalesTestPrice(0.2)}},
	})
	account := &Account{Platform: PlatformSeedance, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test", "video_provider": "fflink_v1",
	}}
	gateway := &OpenAIGatewayService{channelService: channels}
	require.True(t, gateway.isUpstreamModelRestrictedByChannel(ctx, group.ID, account, videoprovider.ModelSeedance20, false))
}

func TestSeedanceSalesUpstreamAliasNormalizationLeavesOtherPlatformsUnchanged(t *testing.T) {
	ctx := context.Background()
	channels := &ChannelService{}
	channels.cache.Store(populateChannelCache([]Channel{{
		ID: 1, GroupIDs: []int64{8}, Status: StatusActive, RestrictModels: true, BillingModelSource: BillingModelSourceUpstream,
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{videoprovider.ModelSeedance25}, BillingMode: BillingModeToken}},
	}}, map[int64]string{8: PlatformOpenAI}))
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"model_mapping": map[string]any{videoprovider.ModelSeedance25: "bytedance/seedance-2.5"},
	}}
	gateway := &OpenAIGatewayService{channelService: channels}
	require.True(t, gateway.isUpstreamModelRestrictedByChannel(ctx, 8, account, videoprovider.ModelSeedance25, false),
		"non-Seedance platforms must retain distinct custom upstream model names")
}

func TestSeedanceSalesRequestEntryCanonicalizesAliasesBeforeRestriction(t *testing.T) {
	ctx := context.Background()
	for _, source := range []string{BillingModelSourceRequested, BillingModelSourceUpstream, BillingModelSourceChannelMapped, BillingModelSourceResponse} {
		t.Run(source, func(t *testing.T) {
			group := seedanceSalesTestGroup()
			channels := seedanceSalesTestService(&Channel{
				Status: StatusActive, RestrictModels: true, BillingModelSource: source,
				ModelPricing: []ChannelModelPricing{{Platform: PlatformSeedance, Models: []string{videoprovider.ModelSeedance25},
					BillingMode: BillingModeVideo, PerRequestPrice: seedanceSalesTestPrice(0.2)}},
			})
			gateway := &OpenAIGatewayService{channelService: channels}
			for _, model := range []string{videoprovider.ModelSeedance25, "bytedance/seedance-2.5", "Seedance-2.5"} {
				_, _, info, err := PrepareSeedanceVideoRequest([]byte(fmt.Sprintf(
					`{"model":%q,"prompt":"waves","duration":10,"resolution":"720p"}`, model)))
				require.NoError(t, err)
				require.Equal(t, videoprovider.ModelSeedance25, info.Model)
				require.False(t, gateway.checkChannelPricingRestriction(ctx, &group.ID, info.Model), model)
			}
		})
	}
}
