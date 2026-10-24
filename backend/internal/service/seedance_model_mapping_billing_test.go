package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

const seedanceMappingPublicModel = "public-seedance"

func seedanceMappingBillingAccount(id int64, provider videoprovider.ID, upstream string, prices ...ChannelModelPricing) *Account {
	capability := videoProtocolCapability(provider)
	capability.Model = seedanceMappingPublicModel
	account := videoTasksGatewayAccount(id, capability, prices...)
	account.Credentials["video_provider"] = string(provider)
	account.Credentials["base_url"] = "https://" + string(provider) + ".example/v1"
	account.Credentials["model_mapping"] = map[string]any{seedanceMappingPublicModel: upstream}
	return account
}

func seedanceMappingBillingTask(t *testing.T, svc *OpenAIGatewayService, key *APIKey, account *Account) *SeedanceVideoPendingBilling {
	t.Helper()
	info := SeedanceVideoRequestInfo{Model: seedanceMappingPublicModel, Resolution: "720p", DurationSeconds: 6}
	if capability, ok := account.VideoModelCapability(seedanceMappingPublicModel); ok {
		info.Capability = &capability
	}
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
	require.NoError(t, err)
	pending := &SeedanceVideoPendingBilling{IDVersion: SeedanceVideoTaskIdentityVersion,
		StateID: "vid_00112233445566778899aabbccddeeff", UserID: key.UserID, APIKeyID: key.ID,
		GroupID: key.GroupID, OriginalModel: seedanceMappingPublicModel, Model: seedanceMappingPublicModel,
		Resolution: "720p", DurationSeconds: 6, HoldID: "mapping-hold", HoldAmount: quote.HoldAmount,
		SalesSnapshot: quote.SalesSnapshot}
	require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
	return pending
}

func seedanceMappingUpstream() *seedanceVideoUpstream {
	return &seedanceVideoUpstream{responses: []*http.Response{{StatusCode: http.StatusOK, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(`{"id":"supplier-task","status":"queued"}`))}}}
}

func TestSeedanceModelMappingBillsPublicSalesAndExactSelectedUpstreamPurchase(t *testing.T) {
	ctx := context.Background()
	accounts := []Account{
		*seedanceMappingBillingAccount(20, videoprovider.ProviderVideoTasksJSONV1, "Seedance-2.0",
			explicitVideoPrice("Seedance-2.0", VideoBillingPerVideo, 0.3), explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 8)),
		*seedanceMappingBillingAccount(21, videoprovider.ProviderZhiqiV1, "seedance-2.0",
			explicitVideoPrice("seedance-2.0", VideoBillingPerVideo, 0.4), explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 9)),
		*seedanceMappingBillingAccount(22, videoprovider.ProviderBBLabuV1, "Seedance-2.0",
			explicitVideoPrice("Seedance-2.0", VideoBillingPerVideo, 0.5), explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 8)),
		*seedanceMappingBillingAccount(23, videoprovider.ProviderFFLinkV1, "seedance-2.0",
			explicitVideoPrice("seedance-2.0", VideoBillingPerVideo, 0.6), explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 9)),
	}
	svc, key := videoTasksGatewayPricingService(explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 0.7),
		explicitVideoPrice("Seedance-2.0", VideoBillingPerVideo, 5), explicitVideoPrice("seedance-2.0", VideoBillingPerVideo, 6))
	svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
	models, err := svc.ListVideoMediaModels(ctx, key)
	require.NoError(t, err)
	require.Len(t, models, 1, "all suppliers must expose one public model")
	require.Equal(t, seedanceMappingPublicModel, models[0].ID)
	request, normalized, info, err := svc.PrepareVideoRequestForKey(ctx, key,
		[]byte(`{"model":"public-seedance","prompt":"waves","duration":6,"resolution":"720p"}`))
	require.NoError(t, err)
	require.Equal(t, seedanceMappingPublicModel, info.Model)
	require.Contains(t, string(normalized), seedanceMappingPublicModel)
	for i := range accounts {
		account := &accounts[i]
		t.Run(string(account.GetVideoProviderID()), func(t *testing.T) {
			svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
			pending := seedanceMappingBillingTask(t, svc, key, account)
			require.NoError(t, svc.AssignSeedanceVideoTaskAccount(ctx, pending, account, string(account.GetVideoProviderID())))
			upstreamModel := account.GetModelMapping()[seedanceMappingPublicModel]
			require.Equal(t, upstreamModel, pending.ExecutionSnapshot.UpstreamModel)
			prepared, err := svc.PrepareVideoRequestForAccount(request, account)
			require.NoError(t, err)
			upstream := seedanceMappingUpstream()
			svc.httpUpstream = upstream
			created, err := svc.ForwardSeedanceVideo(ctx, nil, account, pending.ProviderID, SeedanceVideoEndpointCreate, "", prepared)
			require.NoError(t, err)
			require.NotNil(t, created)
			require.Len(t, upstream.bodies, 1)
			var wire struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.Unmarshal(upstream.bodies[0], &wire))
			require.Equal(t, upstreamModel, wire.Model, "upstream spelling and case must be preserved")
			pending.TaskID = created.ResponseID
			lease := time.Now().Add(time.Minute)
			pending.LeaseUntil = &lease
			result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{
				VideoCount: 1, Model: "supplier-response-model", UpstreamModel: "supplier-response-model"})
			require.NoError(t, err)
			require.InDelta(t, 1.4, cost.ActualCost, 1e-12)
			require.Equal(t, seedanceMappingPublicModel, result.Model)
			require.Equal(t, upstreamModel, result.UpstreamModel)
			billing, logs := &seedanceVideoBillingRepo{}, &seedanceCostUsageRepo{}
			svc.usageBillingRepo, svc.usageLogRepo, svc.deferredService = billing, logs, &DeferredService{}
			require.NoError(t, svc.RecordSeedanceVideoUsage(ctx, pending, &OpenAIRecordUsageInput{
				Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: account,
				ChannelUsageFields: ChannelUsageFields{OriginalModel: seedanceMappingPublicModel}}))
			require.Len(t, logs.logs, 1)
			require.Equal(t, seedanceMappingPublicModel, logs.logs[0].RequestedModel)
			require.Equal(t, upstreamModel, *logs.logs[0].UpstreamModel)
			require.InDelta(t, 0.3+float64(i)*0.1, *logs.logs[0].AccountStatsCost, 1e-12)
			require.InDelta(t, 1.4, billing.commands[0].BalanceCost, 1e-12)
		})
	}
}

func TestSeedanceModelMappingRejectsMissingUpstreamPurchaseDespitePublicPrice(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1,
		videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		t.Run(string(provider), func(t *testing.T) {
			account := seedanceMappingBillingAccount(20, provider, "Seedance-2.0",
				explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 0.3),
				explicitVideoPrice("seedance-2.0", VideoBillingPerVideo, 0.4))
			svc, key := videoTasksGatewayPricingService(explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 0.7))
			svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
			models, err := svc.ListVideoMediaModels(context.Background(), key)
			require.NoError(t, err)
			require.Empty(t, models, "neither public-name pricing nor a differently cased upstream price makes the account eligible")
			_, _, _, err = svc.PrepareVideoRequestForKey(context.Background(), key,
				[]byte(`{"model":"public-seedance","prompt":"waves","duration":6,"resolution":"720p"}`))
			require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
			svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
			pending := seedanceMappingBillingTask(t, svc, key, account)
			err = svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, string(provider))
			require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
			require.Nil(t, pending.AccountCost)
			require.Nil(t, pending.ExecutionSnapshot)
		})
	}
}

func TestSeedanceModelMappingSalesUsesRequestedModelBeforeMappedIdentity(t *testing.T) {
	svc, key := videoTasksGatewayPricingService(
		explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 0.7),
		explicitVideoPrice("Seedance-2.0", VideoBillingPerVideo, 9))
	info := SeedanceVideoRequestInfo{Model: "Seedance-2.0", RequestedModel: seedanceMappingPublicModel,
		Resolution: "720p", DurationSeconds: 6}
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
	require.NoError(t, err)
	require.NotNil(t, quote.SalesSnapshot)
	require.InDelta(t, 0.7, quote.SalesSnapshot.UnitPrice, 1e-12)
	require.InDelta(t, 1.4, quote.HoldAmount, 1e-12)
	price, err := svc.GetSeedanceVideoSalesPrice(context.Background(), key, info)
	require.NoError(t, err)
	require.NotNil(t, price)
	require.InDelta(t, 0.7, *price, 1e-12)
}

func TestSeedanceModelMappingLegacyAliasKeepsReferenceVideoReservationAndMetering(t *testing.T) {
	ctx := context.Background()
	sales := explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerSecond, 0.1)
	sales.VideoQuantityPolicy = VideoQuantityOutputAndReferenceSeconds
	purchase := explicitVideoPrice("Seedance-2.5", VideoBillingPerSecond, 0.04)
	purchase.VideoQuantityPolicy = VideoQuantityOutputAndReferenceSeconds
	account := seedanceMappingBillingAccount(20, videoprovider.ProviderBBLabuV1, "Seedance-2.5", purchase)
	svc, key := videoTasksGatewayPricingService(sales, explicitVideoPrice("Seedance-2.5", VideoBillingPerSecond, 9))
	key.Group.RateMultiplier = 1
	svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
	_, _, info, err := svc.PrepareVideoRequestForKey(ctx, key,
		[]byte(`{"model":"public-seedance","prompt":"waves","duration":30,"resolution":"720p","referenceVideos":["https://assets.example/reference.mp4"]}`))
	require.NoError(t, err)
	require.Equal(t, seedanceMappingPublicModel, info.Model)
	require.Equal(t, 1, info.ReferenceVideoCount)
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(ctx, key, info)
	require.NoError(t, err)
	require.InDelta(t, 6, quote.HoldAmount, 1e-12, "30 output seconds plus 30 reference seconds must use the public price")
	svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
	pending := &SeedanceVideoPendingBilling{IDVersion: SeedanceVideoTaskIdentityVersion,
		StateID: "vid_00112233445566778899aabbccddeeff", UserID: key.UserID, APIKeyID: key.ID,
		Model: seedanceMappingPublicModel, OriginalModel: seedanceMappingPublicModel, Resolution: "720p",
		DurationSeconds: 30, ReferenceVideoCount: 1, HoldID: "reference-hold", HoldAmount: quote.HoldAmount,
		SalesSnapshot: quote.SalesSnapshot}
	require.NoError(t, svc.BeginSeedanceVideoTask(ctx, pending))
	require.NoError(t, svc.AssignSeedanceVideoTaskAccount(ctx, pending, account, string(videoprovider.ProviderBBLabuV1)))
	_, _, err = BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1, VideoDurationSecondsExact: 30})
	require.ErrorContains(t, err, "metering is unavailable")
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{
		VideoCount: 1, VideoDurationSecondsExact: 30, VideoReferenceInputSecondsExact: 12})
	require.NoError(t, err)
	require.InDelta(t, 4.2, cost.ActualCost, 1e-12)
	input := &OpenAIRecordUsageInput{Result: result, CostOverride: cost}
	require.NoError(t, ApplySeedanceAccountCost(input, pending))
	require.InDelta(t, 1.68, *input.AccountStatsCostOverride, 1e-12)
}

func TestSeedanceModelMappingDoesNotMapAnUpstreamNameTwice(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1,
		videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		t.Run(string(provider), func(t *testing.T) {
			account := seedanceMappingBillingAccount(20, provider, "Seedance-2.0",
				explicitVideoPrice("Seedance-2.0", VideoBillingPerVideo, 0.3),
				explicitVideoPrice("seedance-2.5", VideoBillingPerVideo, 7),
				explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 9))
			account.Credentials["model_mapping"] = map[string]any{
				seedanceMappingPublicModel: "Seedance-2.0", "Seedance-2.0": "seedance-2.5"}
			svc, key := videoTasksGatewayPricingService(explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerVideo, 0.7))
			svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
			request, _, _, err := svc.PrepareVideoRequestForKey(context.Background(), key,
				[]byte(`{"model":"public-seedance","prompt":"waves","duration":6,"resolution":"720p"}`))
			require.NoError(t, err)
			svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
			pending := seedanceMappingBillingTask(t, svc, key, account)
			require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, string(provider)))
			require.Equal(t, "Seedance-2.0", pending.ExecutionSnapshot.UpstreamModel)
			require.InDelta(t, 0.3, pending.AccountCost.UnitPrice, 1e-12)
			prepared, err := svc.PrepareVideoRequestForAccount(request, account)
			require.NoError(t, err)
			upstream := seedanceMappingUpstream()
			svc.httpUpstream = upstream
			_, err = svc.ForwardSeedanceVideo(context.Background(), nil, account, string(provider), SeedanceVideoEndpointCreate, "", prepared)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 1)
			var wire struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.Unmarshal(upstream.bodies[0], &wire))
			require.Equal(t, "Seedance-2.0", wire.Model)
		})
	}
}

func TestSeedanceModelMappingRestoredTaskKeepsFrozenRouteSalesAndPurchase(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1,
		videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		t.Run(string(provider), func(t *testing.T) {
			ctx := context.Background()
			account := seedanceMappingBillingAccount(20, provider, "Seedance-2.0",
				explicitVideoPrice("Seedance-2.0", VideoBillingPerSecond, 0.04),
				explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerSecond, 9))
			svc, key := videoTasksGatewayPricingService(explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerSecond, 0.1))
			svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
			request, _, _, err := svc.PrepareVideoRequestForKey(ctx, key,
				[]byte(`{"model":"public-seedance","prompt":"waves","duration":6,"resolution":"720p"}`))
			require.NoError(t, err)
			svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
			pending := seedanceMappingBillingTask(t, svc, key, account)
			require.NoError(t, svc.AssignSeedanceVideoTaskAccount(ctx, pending, account, string(provider)))
			pending.TaskID = "supplier-task"
			lease := time.Now().Add(time.Minute)
			pending.LeaseUntil = &lease
			serialized, err := json.Marshal(pending)
			require.NoError(t, err)
			var restored SeedanceVideoPendingBilling
			require.NoError(t, json.Unmarshal(serialized, &restored))
			account.Credentials["model_mapping"] = map[string]any{seedanceMappingPublicModel: "replacement-model"}
			account.Credentials["base_url"] = "https://replacement.example/v1"
			account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
			account.Extra = map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{
				explicitVideoPrice("replacement-model", VideoBillingPerSecond, 11)}}
			svc.channelService = seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{
				explicitVideoPrice(seedanceMappingPublicModel, VideoBillingPerSecond, 12)}})
			frozen, err := AccountForSeedanceVideoTask(&restored, account)
			require.NoError(t, err)
			prepared, err := svc.PrepareVideoRequestForAccount(request, frozen)
			require.NoError(t, err)
			upstream := seedanceMappingUpstream()
			svc.httpUpstream = upstream
			_, err = svc.ForwardSeedanceVideo(ctx, nil, frozen, restored.ProviderID, SeedanceVideoEndpointCreate, "", prepared)
			require.NoError(t, err)
			endpoint := "/videos"
			if provider == videoprovider.ProviderFFLinkV1 {
				endpoint = "/videos/generations"
			}
			require.Equal(t, "https://"+string(provider)+".example/v1"+endpoint, upstream.requests[0].URL.String())
			var wire struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.Unmarshal(upstream.bodies[0], &wire))
			require.Equal(t, "Seedance-2.0", wire.Model)
			result, cost, err := BuildSeedanceVideoCompletionBilling(&restored, &OpenAIForwardResult{
				VideoCount: 1, VideoDurationSecondsExact: 4.5, Model: "replacement-model", UpstreamModel: "replacement-model"})
			require.NoError(t, err)
			require.Equal(t, seedanceMappingPublicModel, result.Model)
			require.Equal(t, "Seedance-2.0", result.UpstreamModel)
			require.InDelta(t, 0.9, cost.ActualCost, 1e-12)
			billing, logs := &seedanceVideoBillingRepo{}, &seedanceCostUsageRepo{}
			svc.usageBillingRepo, svc.usageLogRepo, svc.deferredService = billing, logs, &DeferredService{}
			require.NoError(t, svc.RecordSeedanceVideoUsage(ctx, &restored, &OpenAIRecordUsageInput{
				Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: frozen}))
			require.Len(t, logs.logs, 1)
			require.InDelta(t, 0.18, *logs.logs[0].AccountStatsCost, 1e-12)
			require.InDelta(t, 0.9, billing.commands[0].BalanceCost, 1e-12)
		})
	}
}
