//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type videoReviewRepoStub struct {
	SeedanceVideoTaskRepository
	task          *SeedanceVideoPendingBilling
	claims        int
	releaseWrites int
}

func (r *videoReviewRepoStub) GetByStateID(context.Context, string) (*SeedanceVideoPendingBilling, error) {
	return r.task, nil
}
func (r *videoReviewRepoStub) ListNeedingReview(context.Context, int, int) ([]*SeedanceVideoPendingBilling, error) {
	return []*SeedanceVideoPendingBilling{r.task}, nil
}
func (r *videoReviewRepoStub) MarkNeedsReview(_ context.Context, _ string, reason string, deadline time.Time) error {
	r.task.SettlementStatus = SeedanceVideoSettlementNeedsReview
	r.task.ReviewReason = reason
	r.task.ReviewDeadline = &deadline
	return nil
}
func (r *videoReviewRepoStub) MarkNeedsReviewWithLease(ctx context.Context, id, reason string, deadline, _ time.Time) error {
	return r.MarkNeedsReview(ctx, id, reason, deadline)
}
func (r *videoReviewRepoStub) ClaimReviewResolution(context.Context, string, time.Duration) (bool, error) {
	if r.task.SettlementStatus != SeedanceVideoSettlementNeedsReview {
		return false, nil
	}
	r.claims++
	deadline := time.Now().Add(time.Minute)
	r.task.LeaseUntil = &deadline
	r.task.SettlementStatus = SeedanceVideoSettlementProcessing
	return true, nil
}
func (r *videoReviewRepoStub) RecordReviewDecisionWithLease(_ context.Context, _ string, decision VideoTaskReviewDecision, _ time.Time) error {
	r.task.ReviewDecisions = append(r.task.ReviewDecisions, decision)
	return nil
}
func (r *videoReviewRepoStub) ResumeReviewPollingWithLease(context.Context, string, time.Time) error {
	r.task.SettlementStatus = SeedanceVideoSettlementPending
	r.task.ReviewReason = ""
	r.task.LeaseUntil = nil
	return nil
}
func (r *videoReviewRepoStub) MarkReleaseIntentWithLease(_ context.Context, _, status string, _ time.Time) error {
	r.task.UpstreamStatus = status
	return nil
}
func (r *videoReviewRepoStub) MarkReleasedWithStatusWithLease(_ context.Context, _, status string, _ time.Time) error {
	r.releaseWrites++
	r.task.SettlementStatus = SeedanceVideoSettlementReleased
	r.task.UpstreamStatus = status
	return nil
}

type videoReviewBillingStub struct {
	UsageBillingRepository
	releases   int
	releaseErr error
	onRelease  func()
}

func (r *videoReviewBillingStub) ReleaseBatchImageBalance(context.Context, *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	r.releases++
	if r.onRelease != nil {
		r.onRelease()
	}
	return &BatchImageBalanceHoldResult{}, r.releaseErr
}

func TestVideoTaskReviewUnknownSubmissionRetainsHold(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff", CreatedAt: time.Now().Add(-time.Hour).Format(time.RFC3339Nano), HoldAmount: 1}
	repo := &videoReviewRepoStub{task: pending}
	billing := &videoReviewBillingStub{}
	gateway := &OpenAIGatewayService{seedanceVideoTaskRepo: repo, usageBillingRepo: billing}
	recovery := NewSeedanceVideoRecoveryService(gateway, nil, nil, nil, nil)
	require.NoError(t, recovery.processTask(context.Background(), pending))
	require.Equal(t, SeedanceVideoSettlementNeedsReview, pending.SettlementStatus)
	require.Equal(t, "submission_outcome_unknown", pending.ReviewReason)
	require.Equal(t, 0, billing.releases)
	_, err := gateway.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "retry_poll", "check again")
	require.ErrorIs(t, err, ErrVideoTaskReviewConflict)
	require.Zero(t, repo.claims)
}

func TestVideoTaskReviewConfirmedFailureReleasesOnceAndRecordsActor(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{StateID: "review-1", TaskID: "supplier-id", SettlementStatus: SeedanceVideoSettlementNeedsReview, HoldAmount: 1, HoldID: "hold-1", UserID: 7, APIKeyID: 8}
	repo := &videoReviewRepoStub{task: pending}
	billing := &videoReviewBillingStub{}
	gateway := &OpenAIGatewayService{seedanceVideoTaskRepo: repo, usageBillingRepo: billing}
	for i := 0; i < 2; i++ {
		_, err := gateway.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_failed", "confirmed by supplier billing record")
		require.NoError(t, err)
	}
	require.Equal(t, 1, billing.releases)
	require.Len(t, pending.ReviewDecisions, 1)
	require.Equal(t, int64(42), pending.ReviewDecisions[0].ActorID)
	require.Equal(t, SeedanceVideoSettlementReleased, pending.SettlementStatus)
}

func TestVideoTaskReviewAlreadyCapturedReturnsReconciledSettlement(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{StateID: "review-captured", TaskID: "supplier-id",
		SettlementStatus: SeedanceVideoSettlementNeedsReview, HoldAmount: 1, HoldID: "hold-1", UserID: 7, APIKeyID: 8}
	repo := &videoReviewRepoStub{task: pending}
	billing := &videoReviewBillingStub{releaseErr: ErrVideoTaskAlreadyCaptured, onRelease: func() {
		pending.SettlementStatus = SeedanceVideoSettlementSettled
		pending.UpstreamStatus = SeedanceVideoResponseStatusCompleted
		pending.LeaseUntil = nil
	}}
	gateway := &OpenAIGatewayService{seedanceVideoTaskRepo: repo, usageBillingRepo: billing}
	result, err := gateway.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_failed", "supplier report was inconsistent")
	require.NoError(t, err)
	require.Equal(t, SeedanceVideoSettlementSettled, result.SettlementStatus)
	require.Equal(t, SeedanceVideoResponseStatusCompleted, result.UpstreamStatus)
	require.Zero(t, repo.releaseWrites)
	require.Len(t, pending.ReviewDecisions, 1)
}

func TestVideoTaskReviewRetryOnlyResumesKnownTask(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{StateID: "review-2", TaskID: "supplier-id", SettlementStatus: SeedanceVideoSettlementNeedsReview, UpstreamStatus: "completed"}
	repo := &videoReviewRepoStub{task: pending}
	gateway := &OpenAIGatewayService{seedanceVideoTaskRepo: repo}
	_, err := gateway.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "retry_poll", "supplier metering is now available")
	require.NoError(t, err)
	require.Equal(t, SeedanceVideoSettlementPending, pending.SettlementStatus)
	require.Equal(t, "supplier-id", pending.TaskID)
	_, err = gateway.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "confirmed_completed", "operator cannot set billing amount")
	require.ErrorIs(t, err, ErrVideoTaskReviewInvalid)
}

func TestVideoTaskReviewRetryPollsOverdueTaskWithinBoundedWindow(t *testing.T) {
	account := seedanceVideoTestAccount()
	createdAt := time.Now().Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	pending := &SeedanceVideoPendingBilling{
		IDVersion: SeedanceVideoTaskIdentityVersion, StateID: "vid_00112233445566778899aabbccddeeff",
		TaskID: "supplier-id", ProviderID: "bblabu_v1", AccountID: account.ID,
		CreatedAt: createdAt, SettlementStatus: SeedanceVideoSettlementNeedsReview,
		ReviewReason: "provider_outcome_overdue", HoldID: "hold-overdue", HoldAmount: 1,
		UserID: 7, APIKeyID: 8,
	}
	repo, billing := &videoReviewRepoStub{task: pending}, &videoReviewBillingStub{}
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"supplier-id","status":"failed"}`)),
	}}}
	gateway := &OpenAIGatewayService{seedanceVideoTaskRepo: repo, usageBillingRepo: billing,
		httpUpstream: upstream, cfg: &config.Config{}}
	_, err := gateway.ResolveSeedanceVideoTaskReview(context.Background(), pending.StateID, 42, "retry_poll", "supplier status is now available")
	require.NoError(t, err)
	lease := time.Now().Add(time.Minute)
	pending.LeaseUntil, pending.SettlementStatus = &lease, SeedanceVideoSettlementProcessing
	recovery := NewSeedanceVideoRecoveryService(gateway, nil, schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}, nil, nil)
	require.NoError(t, recovery.processTask(context.Background(), pending))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Equal(t, SeedanceVideoSettlementReleased, pending.SettlementStatus)
	require.Equal(t, createdAt, pending.CreatedAt)
}

func TestVideoTaskReviewRetryExpiredWindowReturnsOverdueTaskToReview(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{
		IDVersion: SeedanceVideoTaskIdentityVersion, StateID: "vid_00112233445566778899aabbccddeeff",
		TaskID: "supplier-id", CreatedAt: time.Now().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano),
		SettlementStatus: SeedanceVideoSettlementProcessing,
		ReviewDecisions:  []VideoTaskReviewDecision{{Action: "retry_poll", DecidedAt: time.Now().Add(-seedanceRecoveryReviewRetryWindow - time.Second)}},
	}
	repo, billing := &videoReviewRepoStub{task: pending}, &videoReviewBillingStub{}
	recovery := NewSeedanceVideoRecoveryService(&OpenAIGatewayService{seedanceVideoTaskRepo: repo, usageBillingRepo: billing}, nil, nil, nil, nil)
	require.NoError(t, recovery.processTask(context.Background(), pending))
	require.Equal(t, SeedanceVideoSettlementNeedsReview, pending.SettlementStatus)
	require.Equal(t, "provider_outcome_overdue", pending.ReviewReason)
	require.Zero(t, billing.releases)
}
