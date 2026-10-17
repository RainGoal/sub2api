package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

type videoTasksGatewayRepo struct {
	seedanceVideoTaskMemoryRepo
	metering json.RawMessage
}

func (r *videoTasksGatewayRepo) AssignAccountWithExecution(ctx context.Context, stateID string, accountID int64, providerID string, cost *SeedanceAccountCostSnapshot, execution *VideoTasksExecutionSnapshot, _ string) error {
	if err := r.AssignAccount(ctx, stateID, accountID, providerID, cost); err != nil {
		return err
	}
	r.tasks[stateID].ExecutionSnapshot = execution
	return nil
}

func (r *videoTasksGatewayRepo) SaveVideoTaskMeteringWithLease(_ context.Context, _ string, metering json.RawMessage, _ time.Time) error {
	r.metering = append(json.RawMessage(nil), metering...)
	return nil
}

func (r *videoTasksGatewayRepo) GetVideoTaskMetering(_ context.Context, _ string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), r.metering...), nil
}

func videoTasksGatewayPricingService(prices ...ChannelModelPricing) (*OpenAIGatewayService, *APIKey) {
	group := seedanceSalesTestGroup()
	group.RateMultiplier = 2
	key := &APIKey{ID: 8, UserID: 7, User: &User{ID: 7}, Group: group, GroupID: &group.ID}
	cfg := &config.Config{}
	cfg.Gateway.VideoTasksJSONV1Enabled = true
	svc := &OpenAIGatewayService{cfg: cfg, billingService: &BillingService{},
		channelService: seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: prices})}
	return svc, key
}

func videoTasksGatewayAccount(id int64, capability videoprovider.VideoModelCapability, prices ...ChannelModelPricing) *Account {
	account := seedanceVideoTestAccount()
	account.ID = id
	account.Credentials["video_provider"] = string(videoprovider.ProviderVideoTasksJSONV1)
	account.Credentials["model_mapping"] = map[string]any{capability.Model: capability.Model}
	account.Extra = map[string]any{VideoModelCapabilitiesExtra: []videoprovider.VideoModelCapability{capability}, AccountModelCostPricingExtraKey: prices}
	account.RateMultiplier = seedanceCostPrice(9)
	return account
}

func TestVideoTasksGatewaySettlesAllSalesPurchaseUnitCombinations(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	for _, salesUnit := range []VideoBillingUnit{VideoBillingPerSecond, VideoBillingPerVideo} {
		for _, purchaseUnit := range []VideoBillingUnit{VideoBillingPerSecond, VideoBillingPerVideo} {
			t.Run(string(salesUnit)+"/"+string(purchaseUnit), func(t *testing.T) {
				salesPrice, expectedSales := 0.1, 1.7
				if salesUnit == VideoBillingPerVideo {
					salesPrice, expectedSales = 0.7, 1.4
				}
				purchasePrice, expectedPurchase := 0.04, 0.34
				if purchaseUnit == VideoBillingPerVideo {
					purchasePrice, expectedPurchase = 0.3, 0.3
				}
				svc, key := videoTasksGatewayPricingService(explicitVideoPrice(capability.Model, salesUnit, salesPrice))
				repo := &videoTasksGatewayRepo{}
				svc.seedanceVideoTaskRepo = repo
				account := videoTasksGatewayAccount(20, capability, explicitVideoPrice(capability.Model, purchaseUnit, purchasePrice))
				quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, SeedanceVideoRequestInfo{
					Model: capability.Model, Resolution: "720p", DurationSeconds: 10, Capability: &capability})
				require.NoError(t, err)
				pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff",
					UserID: key.UserID, APIKeyID: key.ID, HoldID: "hold", Model: capability.Model, DurationSeconds: 10,
					Resolution: "720p", SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount}
				require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
				require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, "video_tasks_json_v1"))
				pending.TaskID = "supplier-task"
				lease := time.Now().Add(time.Minute)
				pending.LeaseUntil = &lease
				result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{
					VideoCount: 1, VideoDurationSecondsExact: 8.5})
				require.NoError(t, err)
				require.InDelta(t, expectedSales, cost.ActualCost, 1e-12)
				require.Equal(t, pending.PublicID(), result.ResponseID)
				require.Equal(t, StableSeedanceVideoBillingRequestID(pending.PublicID()), result.RequestID)
				billing, logs := &seedanceVideoBillingRepo{}, &seedanceCostUsageRepo{}
				svc.usageBillingRepo, svc.usageLogRepo, svc.deferredService = billing, logs, &DeferredService{}
				require.NoError(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
					Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: account}))
				require.Len(t, logs.logs, 1)
				require.InDelta(t, expectedPurchase, *logs.logs[0].AccountStatsCost, 1e-12)
				require.Equal(t, 1.0, *logs.logs[0].AccountRateMultiplier)
				require.InDelta(t, expectedPurchase, *billing.commands[0].SalesCost, 1e-12)
				require.InDelta(t, expectedSales, billing.commands[0].BalanceCost, 1e-12)
				require.Equal(t, pending.StateID, billing.commands[0].VideoTaskStateID)
				require.Equal(t, pending.LeaseUntil, billing.commands[0].VideoTaskLeaseUntil)
				require.Equal(t, pending.HoldID, billing.commands[0].BalanceHoldID)
				var metering map[string]float64
				require.NoError(t, json.Unmarshal(repo.metering, &metering))
				require.Equal(t, 8.5, metering["output_seconds"])
				require.InDelta(t, expectedSales, metering["sales_cost"], 1e-12)
				require.InDelta(t, expectedPurchase, metering["purchase_cost"], 1e-12)
			})
		}
	}
}

func TestVideoTasksGatewayLegacyPricesKeepProviderFormulaAndEstimatedCost(t *testing.T) {
	svc, key := videoTasksGatewayPricingService()
	key.Group.VideoModelPrices[videoprovider.ModelSeedance25] = map[string]float64{"720p": 0.1}
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, SeedanceVideoRequestInfo{
		Model: videoprovider.ModelSeedance25, Resolution: "720p", DurationSeconds: 10, ReferenceVideoCount: 1})
	require.NoError(t, err)
	require.Nil(t, quote.SalesSnapshot)
	require.Equal(t, 0.1, quote.TotalCostPerSecond)
	require.Equal(t, 0.2, quote.ActualCostPerSecond)
	for _, tc := range []struct {
		provider        string
		sales, purchase float64
	}{{"bblabu_v1", 3, 0.75}, {"fflink_v1", 2, 0.5}} {
		t.Run(tc.provider, func(t *testing.T) {
			svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
			pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff",
				UserID: 7, APIKeyID: 8, HoldID: "legacy-hold", Model: videoprovider.ModelSeedance25,
				Resolution: "720p", DurationSeconds: 10, ReferenceVideoCount: 1, HoldAmount: quote.HoldAmount,
				TotalCostPerSecond: quote.TotalCostPerSecond, ActualCostPerSecond: quote.ActualCostPerSecond}
			require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
			account := seedanceVideoTestAccount()
			account.RateMultiplier = seedanceCostPrice(0.5)
			require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, tc.provider))
			require.NotNil(t, pending.AccountCost)
			require.Zero(t, pending.AccountCost.Version)
			result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{
				VideoCount: 1, VideoDurationSeconds: 10, VideoReferenceInputSeconds: 5})
			require.NoError(t, err)
			require.InDelta(t, tc.sales, cost.ActualCost, 1e-12)
			input := &OpenAIRecordUsageInput{Result: result}
			require.NoError(t, ApplySeedanceAccountCost(input, pending))
			require.InDelta(t, tc.purchase, *input.AccountStatsCostOverride**input.AccountStatsRateMultiplierOverride, 1e-12)
		})
	}
}

func TestVideoTasksGatewayExactMeteringAndHoldLimits(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.5", nil)
	require.True(t, ok)
	row := explicitVideoPrice(capability.Model, VideoBillingPerSecond, 0.1)
	row.VideoQuantityPolicy = VideoQuantityOutputAndReferenceSeconds
	svc, key := videoTasksGatewayPricingService(row)
	info := SeedanceVideoRequestInfo{Model: capability.Model, Resolution: "720p", DurationSeconds: 6, ReferenceVideoCount: 1, Capability: &capability}
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
	require.NoError(t, err)
	require.InDelta(t, 12, quote.HoldAmount, 1e-12)
	pending := &SeedanceVideoPendingBilling{SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount, DurationSeconds: 6,
		ReferenceVideoCount: 1, ExecutionSnapshot: &VideoTasksExecutionSnapshot{Protocol: "video_tasks_json_v1", Capability: &capability}}
	_, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1,
		VideoDurationSecondsExact: 8.5, VideoReferenceInputSecondsExact: 2.75})
	require.NoError(t, err)
	require.InDelta(t, 2.25, cost.ActualCost, 1e-12)
	_, _, err = BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1, VideoDurationSecondsExact: 8.5})
	require.ErrorContains(t, err, "metering is unavailable")
	_, _, err = BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1,
		VideoDurationSecondsExact: 35, VideoReferenceInputSecondsExact: 30})
	require.ErrorContains(t, err, "reserved amount")
	capability.MeteringTrusted = false
	_, err = svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
	require.ErrorContains(t, err, "trusted video metering")
}

func TestVideoTasksGatewayPerVideoAllowsMissingDurationButPerSecondRequiresMetering(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("omni-fast", nil)
	require.True(t, ok)
	row := explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.7)
	svc, key := videoTasksGatewayPricingService(row)
	info := SeedanceVideoRequestInfo{Model: capability.Model, Resolution: "720p", Capability: &capability}
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
	require.NoError(t, err)
	require.InDelta(t, 1.4, quote.HoldAmount, 1e-12)
	pending := &SeedanceVideoPendingBilling{SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount,
		ExecutionSnapshot: &VideoTasksExecutionSnapshot{Protocol: "video_tasks_json_v1", Capability: &capability}}
	_, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1})
	require.NoError(t, err)
	require.InDelta(t, 1.4, cost.ActualCost, 1e-12)
	pending.AccountCost = &SeedanceAccountCostSnapshot{Version: 1, BillingMode: BillingModeVideo,
		BillingUnit: VideoBillingPerSecond, UnitPrice: 0.04, RateMultiplier: 1, QuantityPolicy: VideoQuantityOutputSeconds}
	_, _, err = BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1})
	require.ErrorContains(t, err, "metering is unavailable")
	pending.AccountCost.QuantityPolicy = VideoQuantityRequestedSeconds
	controlled, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	pending.ExecutionSnapshot.Capability = &controlled
	pending.DurationSeconds = 6
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1})
	require.NoError(t, err)
	input := &OpenAIRecordUsageInput{Result: result, CostOverride: cost}
	require.NoError(t, ApplySeedanceAccountCost(input, pending))
	require.InDelta(t, 0.24, *input.AccountStatsCostOverride, 1e-12)
}

func TestVideoTasksGatewayCreateUnknownOutcomeNeverRetriesInForwarder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{{"transport lost", 0, ""}, {"server error", http.StatusInternalServerError, `{"error":"temporary"}`},
		{"invalid JSON", http.StatusOK, `{`}, {"missing identity", http.StatusOK, `{"status":"queued"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := videoTasksGatewayPricingService()
			upstream := &seedanceVideoUpstream{}
			if tc.status != 0 {
				upstream.responses = []*http.Response{{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}}
			}
			svc.httpUpstream = upstream
			request := mustPrepareSeedanceVideoRequest(t, []byte(`{"model":"seedance-2.0","prompt":"waves","duration":10,"resolution":"720p"}`))
			result, err := svc.ForwardSeedanceVideo(context.Background(), nil, seedanceVideoTestAccount(), "bblabu_v1", SeedanceVideoEndpointCreate, "", request)
			require.Nil(t, result)
			var unknown *VideoSubmissionUnknownError
			require.ErrorAs(t, err, &unknown)
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestVideoTasksCatalogKeepsCompleteVariantsAndFiltersMissingPurchase(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	other := capability
	other.MaxDuration, other.AllowedDurations, other.MaxImages, other.MaxTotalAssets = 10, nil, 2, 2
	row := explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.7)
	purchase := explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.3)
	accounts := []Account{*videoTasksGatewayAccount(20, capability, purchase), *videoTasksGatewayAccount(21, other, purchase), *videoTasksGatewayAccount(22, capability)}
	svc, key := videoTasksGatewayPricingService(row)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
	models, err := svc.ListVideoMediaModels(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Len(t, models[0].Variants, 2)
	for _, variant := range models[0].Variants {
		if variant.MaxImages == 1 {
			require.Equal(t, 15, variant.MaxDuration)
		} else {
			require.Equal(t, 2, variant.MaxImages)
			require.Equal(t, 10, variant.MaxDuration)
		}
		require.Len(t, variant.Prices, 2)
		require.Equal(t, VideoBillingPerVideo, variant.Prices[0].BillingUnit)
		require.InDelta(t, 1.4, variant.Prices[0].UnitPrice, 1e-12)
	}
	body, err := json.Marshal(models)
	require.NoError(t, err)
	require.NotContains(t, string(body), "seedance-secret")
	require.NotContains(t, string(body), "api.bblabu")
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts[2:]}
	models, err = svc.ListVideoMediaModels(context.Background(), key)
	require.NoError(t, err)
	require.Empty(t, models)
}
