package service

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func videoProtocolCapability(provider videoprovider.ID) videoprovider.VideoModelCapability {
	profile := videoprovider.ProfileBBLabuOfficial
	if provider == videoprovider.ProviderZhiqiV1 {
		profile = videoprovider.ProfileZhiqiV1
	}
	return videoprovider.VideoModelCapability{Model: "studio-video", Profile: profile, Enabled: true,
		DurationMode: videoprovider.VideoDurationControlled, MinDuration: 1, MaxDuration: 15, DefaultDuration: 6,
		Resolutions: []string{"480p", "720p"}, DefaultResolution: "720p", ResolutionControl: true,
		Ratios: []string{"16:9", "9:16"}, DefaultRatio: "16:9", MaxImages: 2, MaxVideos: 1, MaxAudios: 1, MaxTotalAssets: 4,
		Frames: true, AllowFirstOnly: true, AllowFramesWithReferences: true, GenerateAudio: true,
		BillingUnit: videoprovider.VideoBillingPerSecond, BillingPolicy: videoprovider.BillingOutputOnly,
		MeteringPolicy: videoprovider.VideoMeteringOutputSeconds, MeteringTrusted: true, ContentMode: "content_endpoint"}
}

func videoProtocolAccount(id int64, provider videoprovider.ID, capability videoprovider.VideoModelCapability) *Account {
	account := videoTasksGatewayAccount(id, capability, explicitVideoPrice(string(provider)+"-upstream-model", VideoBillingPerVideo, 0.3))
	account.Credentials["video_provider"] = string(provider)
	account.Credentials["base_url"] = "https://" + string(provider) + ".example/v1"
	account.Credentials["model_mapping"] = map[string]any{capability.Model: string(provider) + "-upstream-model"}
	return account
}

func TestVideoProtocolPublicRequestMapsIndependentlyForMixedSuppliers(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		accounts := []Account{*videoProtocolAccount(20, videoprovider.ProviderVideoTasksJSONV1, videoProtocolCapability(videoprovider.ProviderVideoTasksJSONV1)),
			*videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, videoProtocolCapability(videoprovider.ProviderZhiqiV1))}
		if reverse {
			accounts[0], accounts[1] = accounts[1], accounts[0]
		}
		svc, key := videoTasksGatewayPricingService(explicitVideoPrice("studio-video", VideoBillingPerVideo, 0.7))
		svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
		request, normalized, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p","aspect_ratio":"16:9","referenceImages":["https://assets.example/image.png"],"referenceVideos":["https://assets.example/video.mp4"]}`))
		require.NoError(t, err)
		require.Equal(t, "studio-video", info.Model)
		var canonical struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(normalized, &canonical))
		require.Equal(t, "studio-video", canonical.Model)
		for i := range accounts {
			account := &accounts[i]
			prepared, err := svc.PrepareVideoRequestForAccount(request, account)
			require.NoError(t, err)
			driver, err := videoprovider.Resolve(string(account.GetVideoProviderID()))
			require.NoError(t, err)
			wire, err := driver.BuildRequest(context.Background(), videoprovider.RequestParams{BaseURL: account.GetSeedanceBaseURL(), APIKey: "mock-key", Operation: videoprovider.OperationCreate, CreateRequest: prepared, Capability: prepared.Capability})
			require.NoError(t, err)
			body, err := io.ReadAll(wire.Body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			require.Equal(t, account.GetModelMapping()["studio-video"], payload["model"])
			require.Equal(t, float64(6), payload["duration"])
			require.Equal(t, "720p", payload["resolution"])
			require.Equal(t, "studio-video", request.RequestedModel)
		}
	}
}

func TestVideoProtocolConflictingDefaultsRequireExplicitPublicParameters(t *testing.T) {
	for _, field := range []string{"duration", "resolution", "aspect_ratio"} {
		for _, reverse := range []bool{false, true} {
			t.Run(field+map[bool]string{false: "/json_first", true: "/zhiqi_first"}[reverse], func(t *testing.T) {
				first := videoProtocolCapability(videoprovider.ProviderVideoTasksJSONV1)
				second := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
				switch field {
				case "duration":
					second.DefaultDuration = 10
				case "resolution":
					second.DefaultResolution = "480p"
				case "aspect_ratio":
					second.DefaultRatio = "9:16"
				}
				accounts := []Account{*videoProtocolAccount(20, videoprovider.ProviderVideoTasksJSONV1, first), *videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, second)}
				if reverse {
					accounts[0], accounts[1] = accounts[1], accounts[0]
				}
				svc, key := videoTasksGatewayPricingService(explicitVideoPrice(first.Model, VideoBillingPerVideo, 0.7))
				svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
				request, _, _, err := svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves"}`))
				require.Nil(t, request)
				require.ErrorContains(t, err, "conflicting defaults")
				request, _, _, err = svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p","aspect_ratio":"16:9"}`))
				require.NoError(t, err)
				for i := range accounts {
					prepared, err := svc.PrepareVideoRequestForAccount(request, &accounts[i])
					require.NoError(t, err)
					require.Equal(t, 6, prepared.Duration)
					require.Equal(t, "720p", prepared.Resolution)
					require.Equal(t, "16:9", prepared.AspectRatio)
				}
			})
		}
	}
}

func TestVideoProtocolSalesReservationIsIndependentOfSupplierOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		first := videoProtocolCapability(videoprovider.ProviderVideoTasksJSONV1)
		second := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
		second.MaxDuration = 30
		accounts := []Account{*videoProtocolAccount(20, videoprovider.ProviderVideoTasksJSONV1, first), *videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, second)}
		if reverse {
			accounts[0], accounts[1] = accounts[1], accounts[0]
		}
		svc, key := videoTasksGatewayPricingService(explicitVideoPrice(first.Model, VideoBillingPerSecond, 0.1))
		svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
		_, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves"}`))
		require.NoError(t, err)
		quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
		require.NoError(t, err)
		require.InDelta(t, 6, quote.HoldAmount, 1e-12)
	}
}

func TestVideoProtocolUnpurchasableSupplierDoesNotChangeDefaultsOrReservation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, missingTier := range []bool{false, true} {
			good := videoProtocolCapability(videoprovider.ProviderVideoTasksJSONV1)
			bad := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
			bad.DefaultDuration, bad.MaxDuration = 10, 30
			unavailable := videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, bad)
			delete(unavailable.Extra, AccountModelCostPricingExtraKey)
			if missingTier {
				price := explicitVideoPrice(string(videoprovider.ProviderZhiqiV1)+"-upstream-model", VideoBillingPerVideo, 0.3)
				price.PerRequestPrice = nil
				price.Intervals = []PricingInterval{{TierLabel: "1080p", PerRequestPrice: seedanceCostPrice(0.3)}}
				unavailable.Extra[AccountModelCostPricingExtraKey] = []ChannelModelPricing{price}
			}
			accounts := []Account{*videoProtocolAccount(20, videoprovider.ProviderVideoTasksJSONV1, good), *unavailable}
			if reverse {
				accounts[0], accounts[1] = accounts[1], accounts[0]
			}
			svc, key := videoTasksGatewayPricingService(explicitVideoPrice(good.Model, VideoBillingPerSecond, 0.1))
			svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
			request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves"}`))
			require.NoError(t, err)
			require.Equal(t, 6, request.Duration)
			quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
			require.NoError(t, err)
			require.InDelta(t, 3, quote.HoldAmount, 1e-12)
			svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*unavailable}}
			_, _, _, err = svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves"}`))
			require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
		}
	}
}

func TestVideoProtocolRevalidatesCompleteInputsForSelectedAccount(t *testing.T) {
	first := videoProtocolCapability(videoprovider.ProviderVideoTasksJSONV1)
	first.MaxVideos = 0
	second := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
	accounts := []Account{*videoProtocolAccount(20, videoprovider.ProviderVideoTasksJSONV1, first), *videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, second)}
	svc, key := videoTasksGatewayPricingService(explicitVideoPrice(first.Model, VideoBillingPerVideo, 0.7))
	svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
	request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, []byte(`{"model":"studio-video","prompt":"waves","duration":6,"resolution":"720p","referenceVideos":["https://assets.example/video.mp4"]}`))
	require.NoError(t, err)
	require.Equal(t, 1, info.ReferenceVideoCount)
	_, err = svc.PrepareVideoRequestForAccount(request, &accounts[0])
	require.Error(t, err, "a model name match cannot override the selected account's video input limit")
	prepared, err := svc.PrepareVideoRequestForAccount(request, &accounts[1])
	require.NoError(t, err)
	require.Equal(t, request.ReferenceVideos, prepared.ReferenceVideos)
	require.Equal(t, request.Duration, prepared.Duration)
	require.Equal(t, request.Resolution, prepared.Resolution)
}

func TestVideoProtocolAssignmentFreezesSupplierRouteModelAndCapability(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1} {
		t.Run(string(provider), func(t *testing.T) {
			capability := videoProtocolCapability(provider)
			account := videoProtocolAccount(20, provider, capability)
			svc, key := videoTasksGatewayPricingService(explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.7))
			svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
			quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, SeedanceVideoRequestInfo{
				Model: capability.Model, Resolution: "720p", DurationSeconds: 6, Capability: &capability})
			require.NoError(t, err)
			pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff",
				UserID: 7, APIKeyID: 8, HoldID: "hold", Model: capability.Model, Resolution: "720p", DurationSeconds: 6,
				SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount}
			require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
			require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, string(provider)))
			pending.TaskID = "original-supplier-task"
			require.Equal(t, string(provider)+"-upstream-model", pending.ExecutionSnapshot.UpstreamModel)
			account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
			account.Credentials["base_url"] = "https://changed.example/v1"
			account.Credentials["model_mapping"] = map[string]any{capability.Model: "replacement-model"}
			account.Extra[VideoModelCapabilitiesExtra] = nil
			frozen, err := AccountForSeedanceVideoTask(pending, account)
			require.NoError(t, err)
			require.Equal(t, provider, frozen.GetVideoProviderID())
			require.Equal(t, "https://"+string(provider)+".example/v1", frozen.GetSeedanceBaseURL())
			restored, ok := frozen.VideoModelCapability(capability.Model)
			require.True(t, ok)
			require.Equal(t, capability.MaxVideos, restored.MaxVideos)
			require.Equal(t, pending.ExecutionSnapshot.UpstreamModel, restored.UpstreamModel)
			driver, err := videoprovider.Resolve(pending.ProviderID)
			require.NoError(t, err)
			query, err := driver.BuildRequest(context.Background(), videoprovider.RequestParams{
				BaseURL: frozen.GetSeedanceBaseURL(), APIKey: frozen.GetSeedanceAPIKey(), Operation: videoprovider.OperationStatus, TaskID: pending.UpstreamID()})
			require.NoError(t, err)
			require.Equal(t, frozen.GetSeedanceBaseURL()+"/videos/original-supplier-task", query.URL.String())
			require.Equal(t, "https://changed.example/v1", account.GetSeedanceBaseURL())
		})
	}
}

func TestVideoProtocolActualSecondsAndSettlementRetryRemainProviderNeutral(t *testing.T) {
	for _, tc := range []struct {
		provider videoprovider.ID
		body     string
	}{
		{videoprovider.ProviderVideoTasksJSONV1, `{"id":"supplier-task","status":"completed","seconds":"8.5"}`},
		{videoprovider.ProviderZhiqiV1, `{"id":"supplier-task","status":"completed","usage":{"duration_seconds":8.5}}`},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			svc, pending, key, account, repo, billing := videoMeteringRetryFixture(t, VideoBillingPerSecond)
			capability := videoProtocolCapability(tc.provider)
			pending.ExecutionSnapshot.Protocol, pending.ExecutionSnapshot.Capability = string(tc.provider), &capability
			driver, err := videoprovider.Resolve(string(tc.provider))
			require.NoError(t, err)
			task, err := driver.ParseTask([]byte(tc.body), "supplier-task")
			require.NoError(t, err)
			result, cost, err := BuildSeedanceVideoCompletionBilling(pending, seedanceForwardResultFromTask(task))
			require.NoError(t, err)
			require.Equal(t, 8.5, result.VideoDurationSecondsExact)
			require.InDelta(t, 1.7, cost.ActualCost, 1e-12)
			require.ErrorContains(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
				Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: account}), "temporary payment failure")
			frozen := string(repo.metering)
			for range 2 {
				result, cost, err = svc.BuildSeedanceVideoCompletionBillingWithMetering(context.Background(), pending, &OpenAIForwardResult{VideoCount: 1})
				require.NoError(t, err)
				require.Equal(t, 8.5, result.VideoDurationSecondsExact)
				require.NoError(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
					Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: account}))
			}
			require.JSONEq(t, frozen, string(repo.metering))
			require.Len(t, billing.seen, 1, "retries retain the same billing identity")
			for _, command := range billing.attempts {
				require.Equal(t, StableSeedanceVideoBillingRequestID(pending.PublicID()), command.RequestID)
				require.InDelta(t, 1.7, command.BalanceCost, 1e-12)
				require.InDelta(t, 0.34, *command.SalesCost, 1e-12)
			}
		})
	}
}

func TestVideoProtocolMissingUsageCannotBecomeRequestedDurationCharge(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1} {
		t.Run(string(provider), func(t *testing.T) {
			_, pending, _, _, repo, billing := videoMeteringRetryFixture(t, VideoBillingPerSecond)
			capability := videoProtocolCapability(provider)
			pending.ExecutionSnapshot.Protocol, pending.ExecutionSnapshot.Capability = string(provider), &capability
			driver, err := videoprovider.Resolve(string(provider))
			require.NoError(t, err)
			task, err := driver.ParseTask([]byte(`{"id":"supplier-task","status":"completed","video_url":"https://assets.example/video.mp4"}`), "supplier-task")
			require.NoError(t, err)
			forwarded := seedanceForwardResultFromTask(task)
			forwarded.VideoDurationSeconds = pending.DurationSeconds
			_, _, err = BuildSeedanceVideoCompletionBilling(pending, forwarded)
			require.ErrorContains(t, err, "metering is unavailable", "display/request duration is not actual usage")
			require.Empty(t, repo.metering)
			require.Empty(t, billing.attempts)
		})
	}
}

func TestVideoProtocolSchedulerChannelRestrictionUsesPublicModel(t *testing.T) {
	accounts := []Account{*videoProtocolAccount(20, videoprovider.ProviderVideoTasksJSONV1, videoProtocolCapability(videoprovider.ProviderVideoTasksJSONV1)),
		*videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, videoProtocolCapability(videoprovider.ProviderZhiqiV1))}
	groupID := int64(8)
	for i := range accounts {
		accounts[i].Status, accounts[i].Schedulable = StatusActive, true
		accounts[i].GroupIDs = []int64{groupID}
	}
	for _, allowedModel := range []string{"studio-video", "video_tasks_json_v1-upstream-model"} {
		t.Run(allowedModel, func(t *testing.T) {
			svc, _ := videoTasksGatewayPricingService()
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
			svc.channelService = seedanceSalesTestService(&Channel{Status: StatusActive, RestrictModels: true,
				BillingModelSource: BillingModelSourceUpstream,
				ModelPricing:       []ChannelModelPricing{explicitVideoPrice(allowedModel, VideoBillingPerVideo, 0.7)}})
			for _, wanted := range accounts {
				excluded := map[int64]struct{}{}
				for _, other := range accounts {
					if other.ID != wanted.ID {
						excluded[other.ID] = struct{}{}
					}
				}
				selected, err := svc.selectAccountForModelWithExclusions(context.Background(), &groupID, PlatformSeedance,
					"", "studio-video", excluded, false, 0, "", false)
				if allowedModel != "studio-video" {
					require.ErrorIs(t, err, ErrNoAvailableAccounts, "a supplier's private model price cannot authorize a public model")
					require.Nil(t, selected)
					continue
				}
				require.NoError(t, err, "the public sales model must authorize every explicitly mapped supplier")
				require.NotNil(t, selected)
				require.Equal(t, wanted.ID, selected.ID)
			}
		})
	}
}
