package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestChannelVideoCapabilitiesSharedByCatalogValidationAndSnapshots(t *testing.T) {
	ctx := context.Background()
	capability, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	capability.Model = "custom-video-model"
	capability.ResolutionControl = true
	capability.Resolutions, capability.DefaultResolution = []string{"720p", "1080p"}, "720p"
	capability.MinDuration, capability.MaxDuration, capability.DefaultDuration = 4, 15, 10
	capability.AllowedDurations = nil
	capability.MaxImagesByResolution = nil
	maxDuration, defaultDuration, maxImages := 6, 5, 0
	price := explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.5)
	price.VideoCapabilities = &videoprovider.VideoCapabilityLimits{MaxDuration: &maxDuration, DefaultDuration: &defaultDuration, Resolutions: []string{"720p"}, MaxImages: &maxImages}
	svc, key := videoTasksGatewayPricingService(price)
	account := videoTasksGatewayAccount(20, capability, explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.1))
	svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
	catalog, err := svc.ListVideoMediaModels(ctx, key)
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	require.Equal(t, 6, catalog[0].Variants[0].MaxDuration)
	require.Equal(t, 5, catalog[0].Variants[0].DefaultDuration)
	require.Zero(t, catalog[0].Variants[0].MaxImages)
	request, _, info, err := svc.PrepareVideoRequestForKey(ctx, key, []byte(`{"model":"custom-video-model","prompt":"test"}`))
	require.NoError(t, err)
	require.Equal(t, 5, request.Duration)
	require.NotNil(t, request.ChannelLimits)
	_, _, _, err = svc.PrepareVideoRequestForKey(ctx, key, []byte(`{"model":"custom-video-model","prompt":"test","duration":10}`))
	require.Error(t, err)
	validated, err := svc.PrepareVideoRequestForAccount(request, account)
	require.NoError(t, err)
	require.Equal(t, 6, validated.Capability.MaxDuration)
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(ctx, key, info)
	require.NoError(t, err)
	svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
	pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff", UserID: key.UserID, APIKeyID: key.ID,
		HoldID: "hold", Model: capability.Model, DurationSeconds: request.Duration, Resolution: request.Resolution,
		SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount, ChannelLimits: request.ChannelLimits}
	require.NoError(t, svc.BeginSeedanceVideoTask(ctx, pending))
	require.NoError(t, svc.AssignSeedanceVideoTaskAccount(ctx, pending, account, "video_tasks_json_v1"))
	require.Equal(t, 6, pending.ExecutionSnapshot.Capability.MaxDuration)
	maxDuration = 12
	require.Equal(t, 6, pending.ExecutionSnapshot.Capability.MaxDuration, "existing task keeps its execution snapshot")
	require.Equal(t, 0.5, pending.SalesSnapshot.UnitPrice)
	groupCatalog, err := svc.ListVideoMediaModelsForGroup(ctx, key.Group)
	require.NoError(t, err)
	require.Len(t, groupCatalog, 1, "plaza discovery does not require an API key secret")
}
