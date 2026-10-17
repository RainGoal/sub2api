package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

type videoCatalogAccountRepo struct {
	AccountRepository
	accounts []Account
	groupID  int64
}

func (r *videoCatalogAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	r.groupID = groupID
	return r.accounts, nil
}

func TestVideoMediaCatalogHonorsAuthorizationPricesAndIndependentVariants(t *testing.T) {
	model := "grok-imagine-video-1.5"
	capability, ok := videoprovider.LookupVideoModelCapability(model, nil)
	require.True(t, ok)
	account := seedanceNativeCostAccount(1, explicitVideoPrice(model, VideoBillingPerVideo, 0.03))
	account.Type = AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "never-expose-secret", "video_provider": "video_tasks_json_v1", "base_url": "https://private-supplier.example/v1", "model_mapping": map[string]any{model: model, "omni-fast": "omni-fast"}}
	second := *account
	second.ID = 2
	second.Extra = map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{explicitVideoPrice(model, VideoBillingPerVideo, 0.04)}}
	capability.MaxImages = 2
	capability.MaxImagesByResolution = nil
	second.Extra[VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
	repo := &videoCatalogAccountRepo{accounts: []Account{*account, second}}
	group := seedanceSalesTestGroup()
	group.RateMultiplier = 1
	group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{model}}
	channels := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{explicitVideoPrice(model, VideoBillingPerVideo, 0.5), explicitVideoPrice("omni-fast", VideoBillingPerVideo, 0.6)}})
	cfg := &config.Config{}
	cfg.Gateway.VideoTasksJSONV1Enabled = true
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: repo, billingService: &BillingService{}, channelService: channels}
	key := &APIKey{ID: 1, User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	models, err := svc.ListVideoMediaModels(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, group.ID, repo.groupID)
	require.Len(t, models, 1)
	require.Equal(t, model, models[0].ID)
	require.Len(t, models[0].Variants, 2)
	for _, variant := range models[0].Variants {
		require.Equal(t, VideoBillingPerVideo, variant.Prices[0].BillingUnit)
	}
	encoded, err := json.Marshal(models)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "never-expose")
	require.NotContains(t, string(encoded), "private-supplier")
	require.NotContains(t, string(encoded), "bblabu_route")
	catalogVersion, priceVersion := VideoMediaCatalogVersions(models)
	models[0].Variants[0].MaxImages++
	changedCatalog, samePrices := VideoMediaCatalogVersions(models)
	require.NotEqual(t, catalogVersion, changedCatalog)
	require.Equal(t, priceVersion, samePrices)
	models[0].Variants[0].Prices[0].UnitPrice++
	_, changedPrices := VideoMediaCatalogVersions(models)
	require.NotEqual(t, priceVersion, changedPrices)
	cfg.Gateway.VideoTasksJSONV1Enabled = false
	models, err = svc.ListVideoMediaModels(context.Background(), key)
	require.NoError(t, err)
	require.Empty(t, models)
}

func TestVideoMediaCatalogHidesUnverifiedReferenceVideoInputs(t *testing.T) {
	models := []string{"mm3-minimax-h3-2k", "mm4-minimax-h3-2k"}
	account := seedanceNativeCostAccount(1,
		explicitVideoPrice(models[0], VideoBillingPerVideo, 0.03),
		explicitVideoPrice(models[1], VideoBillingPerVideo, 0.04))
	account.Type = AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "secret", "video_provider": "video_tasks_json_v1",
		"base_url": "https://supplier.example/v1", "model_mapping": map[string]any{models[0]: models[0], models[1]: models[1]}}
	group := seedanceSalesTestGroup()
	group.RateMultiplier = 1
	channels := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{
		explicitVideoPrice(models[0], VideoBillingPerVideo, 0.5), explicitVideoPrice(models[1], VideoBillingPerVideo, 0.6)}})
	cfg := &config.Config{}
	cfg.Gateway.VideoTasksJSONV1Enabled = true
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: &videoCatalogAccountRepo{accounts: []Account{*account}},
		billingService: &BillingService{}, channelService: channels}
	key := &APIKey{ID: 1, User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	catalog, err := svc.ListVideoMediaModels(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, catalog, 2)
	require.Equal(t, models[0], catalog[0].ID)
	variant := catalog[0].Variants[0]
	require.Zero(t, variant.MaxVideos)
	require.False(t, variant.RequireReferenceVideoDurations)
	require.Zero(t, variant.MaxReferenceVideoSeconds)
	require.Zero(t, variant.MaxCombinedVideoSeconds)
	require.Equal(t, 9, variant.MaxImages)
	require.Equal(t, 3, variant.MaxAudios)
	require.Equal(t, 3, catalog[1].Variants[0].MaxVideos)

	capability, ok := videoprovider.LookupVideoModelCapability(models[0], nil)
	require.True(t, ok)
	require.Equal(t, 3, capability.MaxVideos, "public projection must not mutate the stored provider contract")
	capability.MaxReferenceVideoSeconds, capability.MaxCombinedVideoSeconds = 12, 27
	projected := publicVideoVariant(capability)
	require.Zero(t, projected.MaxReferenceVideoSeconds)
	require.Zero(t, projected.MaxCombinedVideoSeconds)
	capability.MaxCombinedVideoSeconds = 10
	require.Equal(t, 10, publicVideoVariant(capability).MaxCombinedVideoSeconds,
		"a bound that still constrains output must remain visible")
	capability.DurationMode, capability.MinDuration, capability.MaxDuration, capability.DefaultDuration = videoprovider.VideoDurationUnknown, 0, 0, 0
	require.Equal(t, 10, publicVideoVariant(capability).MaxCombinedVideoSeconds,
		"an unknown output duration cannot prove the combined limit redundant")
}
