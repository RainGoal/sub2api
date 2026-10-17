package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

type immutableVideoMeteringRepo struct {
	videoTasksGatewayRepo
	readErr error
}

func (r *immutableVideoMeteringRepo) GetVideoTaskMetering(context.Context, string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), r.metering...), r.readErr
}

func (r *immutableVideoMeteringRepo) SaveVideoTaskMeteringWithLease(_ context.Context, _ string, payload json.RawMessage, _ time.Time) error {
	if len(r.metering) > 0 && string(r.metering) != string(payload) {
		return errors.New("immutable metering conflict")
	}
	r.metering = append(json.RawMessage(nil), payload...)
	return nil
}

type retryVideoMeteringBillingRepo struct {
	seedanceVideoBillingRepo
	failures int
	attempts []*UsageBillingCommand
}

func (r *retryVideoMeteringBillingRepo) Apply(ctx context.Context, command *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	command.Normalize()
	copy := *command
	r.attempts = append(r.attempts, &copy)
	if r.failures > 0 {
		r.failures--
		return nil, errors.New("temporary payment failure")
	}
	return r.seedanceVideoBillingRepo.Apply(ctx, command)
}

func videoMeteringRetryFixture(t *testing.T, unit VideoBillingUnit) (*OpenAIGatewayService, *SeedanceVideoPendingBilling, *APIKey, *Account, *immutableVideoMeteringRepo, *retryVideoMeteringBillingRepo) {
	t.Helper()
	capability, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	svc, key := videoTasksGatewayPricingService()
	repo := &immutableVideoMeteringRepo{}
	billing := &retryVideoMeteringBillingRepo{failures: 1}
	svc.seedanceVideoTaskRepo, svc.usageBillingRepo = repo, billing
	svc.usageLogRepo, svc.deferredService = &seedanceCostUsageRepo{}, &DeferredService{}
	policy := VideoQuantityOutputSeconds
	if unit == VideoBillingPerVideo {
		policy = ""
	}
	lease := time.Now().Add(time.Minute)
	pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff", TaskID: "supplier-task",
		UserID: key.UserID, APIKeyID: key.ID, AccountID: 20, Model: capability.Model, Resolution: "720p", DurationSeconds: 10,
		HoldID: "hold", HoldAmount: 10, LeaseUntil: &lease,
		SalesSnapshot:     &VideoTaskSalesSnapshot{Version: 1, BillingUnit: unit, UnitPrice: 0.1, RateMultiplier: 2, QuantityPolicy: policy},
		AccountCost:       &SeedanceAccountCostSnapshot{Version: 1, BillingMode: BillingModeVideo, BillingUnit: unit, UnitPrice: 0.04, RateMultiplier: 1, QuantityPolicy: policy},
		ExecutionSnapshot: &VideoTasksExecutionSnapshot{Version: 1, Protocol: string(videoprovider.ProviderVideoTasksJSONV1), Capability: &capability},
	}
	return svc, pending, key, videoTasksGatewayAccount(20, capability), repo, billing
}

func TestVideoTaskMeteringRetriesReuseFirstSettlementAfterPaymentFailure(t *testing.T) {
	for _, unit := range []VideoBillingUnit{VideoBillingPerVideo, VideoBillingPerSecond} {
		t.Run(string(unit), func(t *testing.T) {
			svc, pending, key, account, repo, billing := videoMeteringRetryFixture(t, unit)
			firstSeconds := 8.5
			if unit == VideoBillingPerVideo {
				firstSeconds = 0
			}
			var finalCost *CostBreakdown
			settle := func(seconds float64) error {
				result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1, VideoDurationSecondsExact: seconds})
				if err != nil {
					return err
				}
				finalCost = cost
				return svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
					Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: account})
			}
			require.ErrorContains(t, settle(firstSeconds), "temporary payment failure")
			original := append(json.RawMessage(nil), repo.metering...)
			require.NotEmpty(t, original)
			require.NoError(t, settle(9.5))
			require.NoError(t, settle(9.75))
			require.JSONEq(t, string(original), string(repo.metering))
			require.Len(t, billing.attempts, 3)
			for _, command := range billing.attempts[1:] {
				require.Equal(t, billing.attempts[0].RequestFingerprint, command.RequestFingerprint)
				require.Equal(t, billing.attempts[0].BalanceCost, command.BalanceCost)
				require.Equal(t, *billing.attempts[0].SalesCost, *command.SalesCost)
			}
			require.Len(t, billing.seen, 1)
			require.Equal(t, billing.attempts[0].BalanceCost, QuantizeUsageBillingAmount(finalCost.ActualCost), "the caller's completion cost must also retain the frozen amount")
			require.Empty(t, billing.releases)
		})
	}
}

func TestVideoTaskMeteringBuildReusesFrozenSecondsWhenProviderOmitsThem(t *testing.T) {
	svc, pending, key, account, repo, _ := videoMeteringRetryFixture(t, VideoBillingPerSecond)
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1, VideoDurationSecondsExact: 8.5})
	require.NoError(t, err)
	require.ErrorContains(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
		Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: account}), "temporary payment failure")
	original := string(repo.metering)
	_, _, err = BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1})
	require.ErrorContains(t, err, "metering is unavailable")
	result, replayedCost, err := svc.BuildSeedanceVideoCompletionBillingWithMetering(context.Background(), pending, &OpenAIForwardResult{VideoCount: 1})
	require.NoError(t, err)
	require.Equal(t, 8.5, result.VideoDurationSecondsExact)
	require.Equal(t, cost.ActualCost, replayedCost.ActualCost)
	require.NoError(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
		Result: result, CostOverride: replayedCost, APIKey: key, User: key.User, Account: account}))
	require.JSONEq(t, original, string(repo.metering))
}

func TestVideoTaskMeteringRejectsInvalidFrozenValuesBeforePayment(t *testing.T) {
	for _, payload := range []string{
		`{`, `null`,
		`{"version":2,"requested_seconds":10}`,
		`{"version":1,"requested_seconds":10,"output_seconds":-1}`,
		`{"version":1,"requested_seconds":10,"output_seconds":1e999}`,
		`{"version":1,"requested_seconds":10,"sales_quantity":2,"purchase_quantity":1,"sales_cost":0.2,"purchase_cost":0.04}`,
		`{"version":1,"requested_seconds":10,"sales_quantity":1,"purchase_quantity":1,"sales_cost":0.3,"purchase_cost":0.04}`,
		`{"version":1,"requested_seconds":10,"sales_quantity":1,"purchase_quantity":1,"sales_cost":0.2,"purchase_cost":0.05}`,
	} {
		t.Run(payload, func(t *testing.T) {
			svc, pending, key, account, repo, billing := videoMeteringRetryFixture(t, VideoBillingPerVideo)
			repo.metering = json.RawMessage(payload)
			result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{VideoCount: 1})
			require.NoError(t, err)
			require.ErrorIs(t, svc.RecordSeedanceVideoUsage(context.Background(), pending, &OpenAIRecordUsageInput{
				Result: result, CostOverride: cost, APIKey: key, User: key.User, Account: account}), ErrVideoTaskMeteringUnavailable)
			require.Empty(t, billing.attempts)
			require.Empty(t, billing.releases)
		})
	}
}

func TestVideoTaskMeteringReaderFailureRemainsRetryableInRecovery(t *testing.T) {
	svc, pending, _, account, repo, billing := videoMeteringRetryFixture(t, VideoBillingPerSecond)
	pending.CreatedAt = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	repo.readErr = errors.New("temporary database failure")
	recovery := &SeedanceVideoRecoveryService{gateway: svc}
	err := recovery.settleCompleted(context.Background(), pending, account, &OpenAIForwardResult{VideoCount: 1})
	require.ErrorIs(t, err, ErrVideoTaskMeteringUnavailable)
	require.ErrorContains(t, err, "temporary database failure")
	require.Empty(t, billing.attempts)
	require.Empty(t, billing.releases)
}
