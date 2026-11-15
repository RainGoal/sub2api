//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type videoSuccessReviewRepo struct {
	videoReviewRepoStub
	metering json.RawMessage
	settles  int
}

func (r *videoSuccessReviewRepo) GetVideoTaskMetering(context.Context, string) (json.RawMessage, error) {
	return r.metering, nil
}

func (r *videoSuccessReviewRepo) SaveVideoTaskMeteringWithLease(_ context.Context, _ string, value json.RawMessage, _ time.Time) error {
	if len(r.metering) > 0 && string(r.metering) != string(value) {
		return errors.New("frozen metering changed")
	}
	r.metering = append(json.RawMessage(nil), value...)
	return nil
}

func (r *videoSuccessReviewRepo) MarkSettledWithLease(_ context.Context, _ string, _ float64, _ time.Time) error {
	r.settles++
	r.task.SettlementStatus, r.task.UpstreamStatus = SeedanceVideoSettlementSettled, SeedanceVideoResponseStatusCompleted
	r.task.LeaseUntil = nil
	return nil
}

func TestVideoTaskReviewSuccessSettlesVerifiedMeteringWithoutProviderAndRetriesPayment(t *testing.T) {
	for _, unit := range []VideoBillingUnit{VideoBillingPerSecond, VideoBillingPerVideo} {
		t.Run(string(unit), func(t *testing.T) {
			svc, pending, key, account, _, billing := videoMeteringRetryFixture(t, unit)
			pending.CreatedAt = time.Now().Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)
			pending.SettlementStatus, pending.ReviewReason = SeedanceVideoSettlementNeedsReview, "completion_metering_unavailable"
			pending.ExecutionSnapshot.Capability.MeteringTrusted = false
			repo := &videoSuccessReviewRepo{videoReviewRepoStub: videoReviewRepoStub{task: pending}}
			svc.seedanceVideoTaskRepo = repo
			metering := &VideoTaskReviewMetering{OutputSeconds: 8.5}
			if unit == VideoBillingPerVideo {
				metering.OutputSeconds = 0
			}
			for i := 0; i < 2; i++ {
				result, err := svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_succeeded", "verified supplier output", metering)
				require.NoError(t, err)
				require.Equal(t, SeedanceVideoSettlementPending, result.SettlementStatus)
			}
			require.Len(t, pending.ReviewDecisions, 1)
			require.Equal(t, int64(42), pending.ReviewDecisions[0].ActorID)
			require.Equal(t, *metering, *pending.ReviewDecisions[0].Metering)
			require.Empty(t, pending.ReviewReason)
			require.Len(t, billing.attempts, 0)
			recovery := NewSeedanceVideoRecoveryService(svc, seedanceVideoAPIKeyRepo{apiKey: key}, schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}, nil, nil)
			lease := time.Now().Add(time.Minute)
			pending.LeaseUntil, pending.SettlementStatus = &lease, SeedanceVideoSettlementProcessing
			require.ErrorContains(t, recovery.processTask(context.Background(), pending), "temporary payment failure")
			frozen := string(repo.metering)
			require.NotEmpty(t, frozen)
			require.NoError(t, recovery.processTask(context.Background(), pending))
			require.Equal(t, SeedanceVideoSettlementSettled, pending.SettlementStatus)
			require.Equal(t, SeedanceVideoResponseStatusCompleted, pending.UpstreamStatus)
			require.JSONEq(t, frozen, string(repo.metering))
			require.Len(t, billing.attempts, 2)
			require.Equal(t, billing.attempts[0].RequestFingerprint, billing.attempts[1].RequestFingerprint)
			require.Len(t, billing.seen, 1)
			require.Empty(t, billing.releases)
			_, err := svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_succeeded", "retry response", metering)
			require.NoError(t, err)
			_, err = svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_failed", "late conflicting result")
			require.ErrorIs(t, err, ErrVideoTaskReviewConflict)
			require.Equal(t, 1, repo.claims)
			require.Equal(t, 1, repo.settles)
		})
	}
}

func TestVideoTaskReviewRejectsInvalidMeteringBeforeClaim(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metering *VideoTaskReviewMetering
		change   func(*SeedanceVideoPendingBilling)
	}{
		{name: "missing"},
		{name: "negative", metering: &VideoTaskReviewMetering{OutputSeconds: -1}},
		{name: "nonfinite", metering: &VideoTaskReviewMetering{OutputSeconds: math.Inf(1)}},
		{name: "zero_actual", metering: &VideoTaskReviewMetering{}},
		{name: "missing_reference", metering: &VideoTaskReviewMetering{OutputSeconds: 8}, change: func(p *SeedanceVideoPendingBilling) {
			p.ReferenceVideoCount, p.AccountCost.QuantityPolicy = 1, VideoQuantityOutputAndReferenceSeconds
		}},
		{name: "duration_limit", metering: &VideoTaskReviewMetering{OutputSeconds: 8}, change: func(p *SeedanceVideoPendingBilling) {
			p.AccountCost.MaxBillableSeconds = 5
		}},
		{name: "hold_limit", metering: &VideoTaskReviewMetering{OutputSeconds: 8}, change: func(p *SeedanceVideoPendingBilling) {
			p.HoldAmount = 1
		}},
		{name: "legacy_fraction", metering: &VideoTaskReviewMetering{OutputSeconds: 8.5}, change: func(p *SeedanceVideoPendingBilling) {
			p.SalesSnapshot = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, pending, _, _, _, billing := videoMeteringRetryFixture(t, VideoBillingPerSecond)
			pending.SettlementStatus = SeedanceVideoSettlementNeedsReview
			if tc.change != nil {
				tc.change(pending)
			}
			repo := &videoSuccessReviewRepo{videoReviewRepoStub: videoReviewRepoStub{task: pending}}
			svc.seedanceVideoTaskRepo = repo
			_, err := svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_succeeded", "verified", tc.metering)
			require.ErrorIs(t, err, ErrVideoTaskReviewMeteringInvalid)
			require.Zero(t, repo.claims)
			require.Empty(t, pending.ReviewDecisions)
			require.Empty(t, billing.attempts)
		})
	}
}

func TestVideoTaskReviewSuccessRequiresKnownUnreleasedTask(t *testing.T) {
	for _, status := range []string{"", "failed", "canceled", "cancel_requested", "released"} {
		t.Run(status, func(t *testing.T) {
			svc, pending, _, _, _, _ := videoMeteringRetryFixture(t, VideoBillingPerVideo)
			pending.SettlementStatus, pending.UpstreamStatus = SeedanceVideoSettlementNeedsReview, status
			if status == "" {
				pending.TaskID = ""
			}
			if status == "released" {
				pending.SettlementStatus = SeedanceVideoSettlementReleased
			}
			repo := &videoSuccessReviewRepo{videoReviewRepoStub: videoReviewRepoStub{task: pending}}
			svc.seedanceVideoTaskRepo = repo
			_, err := svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_succeeded", "verified", &VideoTaskReviewMetering{})
			require.ErrorIs(t, err, ErrVideoTaskReviewConflict)
			require.Zero(t, repo.claims)
		})
	}
}

func TestVideoTaskReviewSuccessPreservesDifferentSalesAndPurchaseQuantities(t *testing.T) {
	svc, pending, key, account, _, billing := videoMeteringRetryFixture(t, VideoBillingPerVideo)
	billing.failures = 0
	pending.SettlementStatus = SeedanceVideoSettlementNeedsReview
	pending.ReferenceVideoCount = 1
	pending.AccountCost.BillingUnit = VideoBillingPerSecond
	pending.AccountCost.QuantityPolicy = VideoQuantityOutputAndReferenceSeconds
	repo := &videoSuccessReviewRepo{videoReviewRepoStub: videoReviewRepoStub{task: pending}}
	svc.seedanceVideoTaskRepo = repo
	metering := &VideoTaskReviewMetering{OutputSeconds: 8.5, ReferenceSeconds: 2.25}
	_, err := svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_succeeded", "supplier invoice verified", metering)
	require.NoError(t, err)
	lease := time.Now().Add(time.Minute)
	pending.LeaseUntil, pending.SettlementStatus = &lease, SeedanceVideoSettlementProcessing
	recovery := NewSeedanceVideoRecoveryService(svc, seedanceVideoAPIKeyRepo{apiKey: key}, schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}, nil, nil)
	require.NoError(t, recovery.processTask(context.Background(), pending))
	var frozen videoTaskMeteringSnapshot
	require.NoError(t, json.Unmarshal(repo.metering, &frozen))
	require.Equal(t, 1.0, frozen.SalesQuantity)
	require.Equal(t, 10.75, frozen.PurchaseQuantity)
	require.InDelta(t, 0.2, frozen.SalesCost, 1e-9)
	require.InDelta(t, 0.43, *frozen.PurchaseCost, 1e-9)
}

func TestVideoTaskReviewSuccessCannotReplaceFrozenMetering(t *testing.T) {
	svc, pending, _, _, source, _ := videoMeteringRetryFixture(t, VideoBillingPerSecond)
	pending.SettlementStatus = SeedanceVideoSettlementNeedsReview
	source.metering = json.RawMessage(`{"version":1,"output_seconds":8.5,"reference_seconds":0,"requested_seconds":10,"sales_quantity":8.5,"purchase_quantity":8.5,"sales_cost":1.7000000000000002,"purchase_cost":0.34}`)
	repo := &videoSuccessReviewRepo{videoReviewRepoStub: videoReviewRepoStub{task: pending}, metering: source.metering}
	svc.seedanceVideoTaskRepo = repo
	_, err := svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_succeeded", "different duration", &VideoTaskReviewMetering{OutputSeconds: 9})
	require.ErrorIs(t, err, ErrVideoTaskReviewMeteringInvalid)
	require.Zero(t, repo.claims)
	_, err = svc.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_succeeded", "same duration", &VideoTaskReviewMetering{OutputSeconds: 8.5})
	require.NoError(t, err)
}
