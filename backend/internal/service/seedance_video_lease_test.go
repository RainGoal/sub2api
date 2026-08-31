package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seedanceVideoLeaseProbeRepo keeps the ordinary in-memory test repository
// behavior while recording which owner-checked mutation the service selects.
type seedanceVideoLeaseProbeRepo struct {
	seedanceVideoTaskMemoryRepo
	rescheduleLease     time.Time
	settledLease        time.Time
	releasedLease       time.Time
	releasedStatusLease time.Time
	releasedStatus      string
}

// seedanceVideoBaseOnlyRepo hides optional methods from the service so the
// fail-closed behavior can be tested independently of the richer memory repo.
type seedanceVideoBaseOnlyRepo struct {
	SeedanceVideoTaskRepository
}

func (r *seedanceVideoLeaseProbeRepo) RescheduleWithLease(ctx context.Context, stateID, status string, dueAt time.Time, lastError string, leaseUntil time.Time) error {
	r.rescheduleLease = leaseUntil
	return r.Reschedule(ctx, stateID, status, dueAt, lastError)
}

func (r *seedanceVideoLeaseProbeRepo) MarkSettledWithLease(ctx context.Context, stateID string, actualCost float64, leaseUntil time.Time) error {
	r.settledLease = leaseUntil
	return r.MarkSettled(ctx, stateID, actualCost)
}

func (r *seedanceVideoLeaseProbeRepo) MarkReleasedWithLease(ctx context.Context, stateID string, leaseUntil time.Time) error {
	r.releasedLease = leaseUntil
	return r.MarkReleased(ctx, stateID)
}

func (r *seedanceVideoLeaseProbeRepo) MarkReleasedWithStatusWithLease(ctx context.Context, stateID, status string, leaseUntil time.Time) error {
	r.releasedStatusLease = leaseUntil
	r.releasedStatus = status
	return r.MarkReleasedWithStatus(ctx, stateID, status)
}

func seedanceVideoLeaseProbePending(stateID string, leaseUntil time.Time) *SeedanceVideoPendingBilling {
	return &SeedanceVideoPendingBilling{
		StateID: "state-" + stateID, TaskID: "task-" + stateID, UserID: 7, APIKeyID: 8,
		Model: "Seedance-2.0", Resolution: "720p", DurationSeconds: 10,
		UpstreamStatus: "running", SettlementStatus: SeedanceVideoSettlementProcessing,
		LeaseUntil: &leaseUntil,
	}
}

func TestSeedanceVideoLeaseAwareServiceMutationsUseClaimToken(t *testing.T) {
	leaseUntil := time.Now().UTC().Add(time.Minute)
	repo := &seedanceVideoLeaseProbeRepo{seedanceVideoTaskMemoryRepo: seedanceVideoTaskMemoryRepo{
		tasks: make(map[string]*SeedanceVideoPendingBilling),
	}}
	svc := &OpenAIGatewayService{seedanceVideoTaskRepo: repo}
	ctx := context.Background()

	reschedulePending := seedanceVideoLeaseProbePending("reschedule", leaseUntil)
	repo.tasks[reschedulePending.StateID] = reschedulePending
	require.NoError(t, svc.RescheduleSeedanceVideoTaskWithError(ctx, reschedulePending, time.Now().Add(time.Minute), "retry"))
	require.Equal(t, leaseUntil, repo.rescheduleLease)

	settlePending := seedanceVideoLeaseProbePending("settle", leaseUntil)
	repo.tasks[settlePending.StateID] = settlePending
	require.NoError(t, svc.CompleteSeedanceVideoTask(ctx, settlePending, 1.25))
	require.Equal(t, leaseUntil, repo.settledLease)

	releasePending := seedanceVideoLeaseProbePending("release", leaseUntil)
	repo.tasks[releasePending.StateID] = releasePending
	require.NoError(t, svc.ReleaseSeedanceVideoTaskWithStatus(ctx, releasePending, "failed"))
	require.Equal(t, leaseUntil, repo.releasedStatusLease)
	require.Equal(t, "failed", repo.releasedStatus)
}

func TestRetrySeedanceVideoBillingWithLeaseFailsClosedWithoutOwnerToken(t *testing.T) {
	repo := &seedanceVideoTaskMemoryRepo{tasks: make(map[string]*SeedanceVideoPendingBilling)}
	svc := &OpenAIGatewayService{seedanceVideoTaskRepo: repo}
	pending := &SeedanceVideoPendingBilling{
		StateID: "state-no-owner", TaskID: "task-no-owner",
		SettlementStatus: SeedanceVideoSettlementProcessing,
	}

	err := svc.RetrySeedanceVideoBillingWithLease(context.Background(), pending, "retry")

	require.ErrorIs(t, err, ErrSeedanceVideoLeaseMutationUnsupported)
	require.Equal(t, SeedanceVideoSettlementProcessing, pending.SettlementStatus)
}

func TestSeedanceVideoLeaseAwareServiceFailsClosedWithoutExtension(t *testing.T) {
	leaseUntil := time.Now().UTC().Add(time.Minute)
	pending := seedanceVideoLeaseProbePending("unsupported", leaseUntil)
	baseRepo := &seedanceVideoTaskMemoryRepo{tasks: map[string]*SeedanceVideoPendingBilling{
		pending.StateID: pending,
	}}
	repo := &seedanceVideoBaseOnlyRepo{SeedanceVideoTaskRepository: baseRepo}
	svc := &OpenAIGatewayService{seedanceVideoTaskRepo: repo}

	err := svc.CompleteSeedanceVideoTask(context.Background(), pending, 1)
	require.ErrorIs(t, err, ErrSeedanceVideoLeaseMutationUnsupported)
	require.Equal(t, SeedanceVideoSettlementProcessing, pending.SettlementStatus)
}

func TestSeedanceVideoMemoryLeaseMutationsRejectMismatchAndExpiry(t *testing.T) {
	repo := &seedanceVideoTaskMemoryRepo{tasks: make(map[string]*SeedanceVideoPendingBilling)}
	validLease := time.Now().UTC().Add(time.Minute)
	pending := seedanceVideoLeaseProbePending("mismatch", validLease)
	repo.tasks[pending.StateID] = pending

	err := repo.MarkSettledWithLease(context.Background(), pending.StateID, 1, validLease.Add(time.Second))
	require.ErrorIs(t, err, ErrSeedanceVideoTaskNotFound)
	require.Equal(t, SeedanceVideoSettlementProcessing, pending.SettlementStatus)

	expiredLease := time.Now().UTC().Add(-time.Second)
	expired := seedanceVideoLeaseProbePending("expired", expiredLease)
	repo.tasks[expired.StateID] = expired
	err = repo.MarkReleasedWithStatusWithLease(context.Background(), expired.StateID, "failed", expiredLease)
	require.ErrorIs(t, err, ErrSeedanceVideoTaskNotFound)
	require.Equal(t, SeedanceVideoSettlementProcessing, expired.SettlementStatus)
}
