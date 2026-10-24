package service

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func mappedLegacyVideoAccount(provider, target string) *Account {
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = provider
	account.Credentials["model_mapping"] = map[string]any{"studio-video": target}
	account.Extra = map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{
		explicitVideoPrice(target, VideoBillingPerVideo, 0.3),
	}}
	return account
}

func TestVideoMappingLegacyRequestUsesPublicSaleAndRevalidatesEachTarget(t *testing.T) {
	first := mappedLegacyVideoAccount("bblabu_v1", "Seedance-2.0")
	second := mappedLegacyVideoAccount("fflink_v1", "seedance-2.5")
	svc, key := videoTasksGatewayPricingService(explicitVideoPrice("studio-video", VideoBillingPerVideo, 0.7))
	svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*first, *second}}
	request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key,
		[]byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p"}`))
	require.NoError(t, err)
	require.Equal(t, "studio-video", info.Model)
	require.Equal(t, "studio-video", request.RequestedModel)
	require.Equal(t, "studio-video", request.Raw["model"])
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
	require.NoError(t, err)
	require.Equal(t, 0.7, quote.SalesSnapshot.UnitPrice)
	for _, account := range []*Account{first, second, first} {
		prepared, err := svc.PrepareVideoRequestForAccount(request, account)
		require.NoError(t, err)
		require.Equal(t, account.GetModelMapping()["studio-video"], prepared.UpstreamModel)
		require.Equal(t, "studio-video", prepared.RequestedModel)
		driver, err := videoprovider.Resolve(string(account.GetVideoProviderID()))
		require.NoError(t, err)
		wire, err := driver.BuildRequest(context.Background(), videoprovider.RequestParams{
			BaseURL: account.GetSeedanceBaseURL(), APIKey: "test-key", Operation: videoprovider.OperationCreate, CreateRequest: prepared})
		require.NoError(t, err)
		body, err := io.ReadAll(wire.Body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, prepared.UpstreamModel, payload["model"])
		request = prepared
	}
	request.Duration = 20
	_, err = svc.PrepareVideoRequestForAccount(request, first)
	require.ErrorContains(t, err, "between 4 and 15")
	_, err = svc.PrepareVideoRequestForAccount(request, second)
	require.NoError(t, err)
}

func TestVideoMappingLegacyAliasWorksWithCapabilityProtocolDisabled(t *testing.T) {
	account := mappedLegacyVideoAccount("bblabu_v1", "Seedance-2.0")
	account.Credentials["model_mapping"] = map[string]any{"seedance-2.0": "Seedance-2.0"}
	sales := ChannelModelPricing{Platform: PlatformSeedance, Models: []string{"seedance-2.0"},
		BillingMode: BillingModeVideo, PerRequestPrice: seedanceCostPrice(0.1)}
	svc, key := videoTasksGatewayPricingService(sales)
	svc.cfg.Gateway.VideoTasksJSONV1Enabled = false
	svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
	request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key,
		[]byte(`{"model":"seedance-2.0","prompt":"waves"}`))
	require.NoError(t, err)
	require.Equal(t, "seedance-2.0", info.Model)
	require.Equal(t, "Seedance-2.0", info.UpstreamModel)
	require.Equal(t, "Seedance-2.0", request.UpstreamModel)
	require.Equal(t, 4, request.Duration)
}

func TestVideoMappingAdmissionRequiresUpstreamCostInsteadOfPublicCost(t *testing.T) {
	for _, provider := range []string{"bblabu_v1", "fflink_v1", "video_tasks_json_v1", "zhiqi_v1"} {
		t.Run(provider, func(t *testing.T) {
			account := mappedLegacyVideoAccount(provider, "Seedance-2.0")
			if videoprovider.SupportsModelCapabilities(account.GetVideoProviderID()) {
				capability := videoProtocolCapability(account.GetVideoProviderID())
				account.Extra[VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
			}
			account.Extra[AccountModelCostPricingExtraKey] = []ChannelModelPricing{
				explicitVideoPrice("studio-video", VideoBillingPerVideo, 0.01),
			}
			svc, key := videoTasksGatewayPricingService(explicitVideoPrice("studio-video", VideoBillingPerVideo, 0.7))
			svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
			_, _, _, err := svc.PrepareVideoRequestForKey(context.Background(), key,
				[]byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p"}`))
			require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
		})
	}
}

func TestVideoMappingLegacyCatalogListsPublicAlias(t *testing.T) {
	account := mappedLegacyVideoAccount("bblabu_v1", "Seedance-2.0")
	svc := &GatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}}
	groupID := int64(1)
	models := svc.GetAvailableModels(context.Background(), &groupID, PlatformSeedance)
	require.Equal(t, []string{"studio-video"}, models)
}

func TestVideoMappingPublicRequestHashIgnoresSupplierModelAndOrder(t *testing.T) {
	first := mappedLegacyVideoAccount("bblabu_v1", "Seedance-2.0")
	second := mappedLegacyVideoAccount("fflink_v1", "seedance-2.5")
	var previous []byte
	for _, accounts := range [][]Account{{*first, *second}, {*second, *first}} {
		svc, key := videoTasksGatewayPricingService(explicitVideoPrice("studio-video", VideoBillingPerVideo, 0.7))
		svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
		_, normalized, _, err := svc.PrepareVideoRequestForKey(context.Background(), key,
			[]byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p"}`))
		require.NoError(t, err)
		if previous != nil {
			require.Equal(t, previous, normalized)
		}
		previous = normalized
		var payload map[string]any
		require.NoError(t, json.Unmarshal(normalized, &payload))
		require.Equal(t, "studio-video", payload["model"])
	}
}

func TestVideoMappingReservationUsesLargestCompleteSupplierContract(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		accounts := []Account{*mappedLegacyVideoAccount("bblabu_v1", "Seedance-2.0"),
			*mappedLegacyVideoAccount("bblabu_v1", "Seedance-2.5")}
		if mixed {
			capability := videoProtocolCapability(videoprovider.ProviderVideoTasksJSONV1)
			capability.MaxReferenceVideoSeconds = 5
			capability.BillingPolicy = videoprovider.BillingOutputAndReference
			capability.MeteringPolicy = videoprovider.VideoMeteringOutputAndReferenceSeconds
			modern := videoTasksGatewayAccount(30, capability, explicitVideoPrice("Modern/Video", VideoBillingPerVideo, 0.2))
			modern.Credentials["model_mapping"] = map[string]any{"studio-video": "Modern/Video"}
			accounts = append(accounts, *modern)
		}
		for _, reverse := range []bool{false, true} {
			if reverse {
				for left, right := 0, len(accounts)-1; left < right; left, right = left+1, right-1 {
					accounts[left], accounts[right] = accounts[right], accounts[left]
				}
			}
			sales := explicitVideoPrice("studio-video", VideoBillingPerSecond, 0.1)
			sales.VideoQuantityPolicy = VideoQuantityOutputAndReferenceSeconds
			svc, key := videoTasksGatewayPricingService(sales)
			svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
			_, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key,
				[]byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p","referenceVideos":["https://assets.example/reference.mp4"]}`))
			require.NoError(t, err, "mixed=%v reverse=%v", mixed, reverse)
			require.Equal(t, "studio-video", info.Model)
			require.Equal(t, "Seedance-2.5", info.UpstreamModel)
			quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
			require.NoError(t, err)
			require.InDelta(t, 7.2, quote.HoldAmount, 1e-12, "six output plus thirty reference seconds, at the public price")
			require.Equal(t, 0.1, quote.SalesSnapshot.UnitPrice)
		}
	}
}
