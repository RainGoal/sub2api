package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestVideoTasksPurchaseRequestedDurationLimitRejectsBeforeAssignment(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	for _, duration := range []int{5, 15} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			svc, key := videoTasksGatewayPricingService(explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.7))
			repo := &videoTasksGatewayRepo{}
			svc.seedanceVideoTaskRepo = repo
			purchase := explicitVideoPrice(capability.Model, VideoBillingPerSecond, 0.3)
			purchase.VideoQuantityPolicy, purchase.VideoMaxBillableSeconds = VideoQuantityRequestedSeconds, 5
			account := videoTasksGatewayAccount(20, capability, purchase)
			pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff",
				UserID: 7, APIKeyID: 8, HoldID: "hold", Model: capability.Model, Resolution: "720p", DurationSeconds: duration,
				HoldAmount:    0.7,
				SalesSnapshot: &VideoTaskSalesSnapshot{Version: 1, BillingUnit: VideoBillingPerVideo, UnitPrice: 0.7, RateMultiplier: 1}}
			require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
			err := svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, "video_tasks_json_v1")
			if duration > 5 {
				require.ErrorIs(t, err, ErrSeedanceAccountCostMissing)
				require.Zero(t, repo.tasks[pending.StateID].AccountID)
				return
			}
			require.NoError(t, err)
			require.Equal(t, account.ID, repo.tasks[pending.StateID].AccountID)
			require.Equal(t, key.UserID, pending.UserID)
		})
	}
}

func TestVideoTasksSalesKnownDurationLimitRejectsBeforeCreate(t *testing.T) {
	for _, policy := range []VideoQuantityPolicy{VideoQuantityRequestedSeconds, VideoQuantityOutputSeconds, VideoQuantityOutputAndReferenceSeconds} {
		for _, duration := range []int{5, 15} {
			t.Run(fmt.Sprintf("%s/%d", policy, duration), func(t *testing.T) {
				capability, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
				require.True(t, ok)
				row := explicitVideoPrice(capability.Model, VideoBillingPerSecond, 0.1)
				row.VideoQuantityPolicy, row.VideoMaxBillableSeconds = policy, 5
				svc, key := videoTasksGatewayPricingService(row)
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*videoTasksGatewayAccount(20, capability,
					explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.3))}}
				body, err := json.Marshal(map[string]any{"model": capability.Model, "prompt": "waves", "duration": duration, "resolution": "720p"})
				require.NoError(t, err)
				request, _, _, err := svc.PrepareVideoRequestForKey(context.Background(), key, body)
				if duration > 5 {
					require.ErrorContains(t, err, "configured limit")
					require.Nil(t, request)
					return
				}
				require.NoError(t, err)
				require.Equal(t, duration, request.Duration)
			})
		}
	}
}

func TestVideoTasksLegacyExplicitSalesKnownDurationLimitRejectsBeforeCreate(t *testing.T) {
	for _, policy := range []VideoQuantityPolicy{VideoQuantityRequestedSeconds, VideoQuantityOutputSeconds, VideoQuantityOutputAndReferenceSeconds} {
		for _, duration := range []int{5, 15} {
			t.Run(fmt.Sprintf("%s/%d", policy, duration), func(t *testing.T) {
				row := explicitVideoPrice(videoprovider.ModelSeedance25, VideoBillingPerSecond, 0.1)
				row.VideoQuantityPolicy, row.VideoMaxBillableSeconds = policy, 5
				svc, key := videoTasksGatewayPricingService(row)
				quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, SeedanceVideoRequestInfo{
					Model: videoprovider.ModelSeedance25, Resolution: "720p", DurationSeconds: duration})
				if duration > 5 {
					require.ErrorContains(t, err, "configured limit")
					require.Nil(t, quote)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, quote.SalesSnapshot)
			})
		}
	}
}

func TestVideoTasksSalesUnknownDurationPreservesMeteringRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unit    VideoBillingUnit
		policy  VideoQuantityPolicy
		trusted bool
		reject  bool
	}{
		{"trusted_actual", VideoBillingPerSecond, VideoQuantityOutputSeconds, true, false},
		{"untrusted_actual", VideoBillingPerSecond, VideoQuantityOutputSeconds, false, true},
		{"requested_requires_duration", VideoBillingPerSecond, VideoQuantityRequestedSeconds, false, true},
		{"per_video_keeps_unknown", VideoBillingPerVideo, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capability, ok := videoprovider.LookupVideoModelCapability("omni-fast", nil)
			require.True(t, ok)
			capability.MeteringTrusted = tc.trusted
			row := explicitVideoPrice(capability.Model, tc.unit, 0.1)
			row.VideoQuantityPolicy, row.VideoMaxBillableSeconds = tc.policy, 5
			svc, key := videoTasksGatewayPricingService(row)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*videoTasksGatewayAccount(20, capability,
				explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.3))}}
			body, err := json.Marshal(map[string]any{"model": capability.Model, "prompt": "waves"})
			require.NoError(t, err)
			request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, body)
			if tc.reject {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Zero(t, request.Duration)
			require.Zero(t, info.DurationSeconds)
		})
	}
}

func TestPrepareVideoRequestForLegacyAccountPreservesValidatedPayload(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
			for _, style := range []string{"camelCase", "guidances"} {
				t.Run(fmt.Sprintf("%s/%s/enabled=%t", provider, style, enabled), func(t *testing.T) {
					body := map[string]any{"model": "bytedance/seedance-2.5", "prompt": "waves", "duration": 10,
						"resolution": "720p", "audio": true, "watermark": false}
					if style == "camelCase" {
						body["referenceImages"] = []string{"https://assets.example/image.png"}
						body["referenceVideos"] = []string{"https://assets.example/video.mp4"}
						body["referenceAudios"] = []string{"https://assets.example/audio.mp3"}
					} else {
						body["guidances"] = map[string]any{
							"image_reference":      []any{map[string]any{"image": map[string]any{"url": "https://assets.example/image.png"}, "strength": "HIGH"}},
							"video_reference_base": []any{map[string]any{"video": map[string]any{"url": "https://assets.example/video.mp4"}}},
							"audio_reference":      []any{map[string]any{"audio": map[string]any{"url": "https://assets.example/audio.mp3"}}},
						}
					}
					encoded, err := json.Marshal(body)
					require.NoError(t, err)
					svc, key := videoTasksGatewayPricingService()
					svc.cfg.Gateway.VideoTasksJSONV1Enabled = enabled
					account := seedanceVideoTestAccount()
					account.Credentials["video_provider"] = string(provider)
					svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}
					request, _, _, err := svc.PrepareVideoRequestForKey(context.Background(), key, encoded)
					require.NoError(t, err)
					driver, err := videoprovider.Resolve(string(provider))
					require.NoError(t, err)
					wireBody := func(input *SeedanceVideoCreateRequest) []byte {
						req, err := driver.BuildRequest(context.Background(), videoprovider.RequestParams{
							BaseURL: "https://legacy.example/v1", APIKey: "local-mock-key", Operation: videoprovider.OperationCreate, CreateRequest: input})
						require.NoError(t, err)
						payload, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						require.NoError(t, req.Body.Close())
						return payload
					}
					before := wireBody(request)
					prepared, err := svc.PrepareVideoRequestForAccount(request, account)
					require.NoError(t, err)
					require.Equal(t, request.ReferenceImages, prepared.ReferenceImages)
					require.Equal(t, request.ReferenceVideos, prepared.ReferenceVideos)
					require.Equal(t, request.ReferenceAudios, prepared.ReferenceAudios)
					after := wireBody(prepared)
					require.JSONEq(t, string(before), string(after))
					require.Equal(t, request.RequestedModel, prepared.RequestedModel)
					var payload map[string]any
					require.NoError(t, json.Unmarshal(after, &payload))
					require.Equal(t, false, payload["watermark"])
					require.NotContains(t, payload, "raw")
					require.NotContains(t, payload, "requested_model")
				})
			}
		}
	}
}
