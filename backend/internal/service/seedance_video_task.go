package service

import (
	"context"
	"errors"
	"time"
)

const (
	SeedanceVideoSettlementPending           = "pending"
	SeedanceVideoSettlementProcessing        = "processing"
	SeedanceVideoSettlementSettled           = "settled"
	SeedanceVideoSettlementReleased          = "released"
	SeedanceVideoSettlementNeedsReview       = "needs_review"
	SeedanceVideoCancellationRequestedStatus = "cancel_requested"
)

var ErrSeedanceVideoTaskNotFound = errors.New("seedance video task not found")

// ErrSeedanceVideoLeaseMutationUnsupported is returned when a caller still
// holds a lease token but its repository does not expose the corresponding
// owner-checked mutation. Falling back to an unguarded write in that case could
// let an expired worker overwrite a newer claim.
var ErrSeedanceVideoLeaseMutationUnsupported = errors.New("seedance video lease-aware mutation is unsupported")

// SeedanceVideoTaskRepository is the durable accounting boundary for the
// fork-owned provider. Implementations must use database-level claims so the
// recovery worker is safe when multiple Sub2API replicas are running.
type SeedanceVideoTaskRepository interface {
	Create(ctx context.Context, pending *SeedanceVideoPendingBilling) error
	AssignAccount(ctx context.Context, stateID string, accountID int64, providerID string, accountCost *SeedanceAccountCostSnapshot) error
	BindProviderTask(ctx context.Context, stateID, taskID, upstreamStatus string, dueAt time.Time) error
	// GetByProviderTask accepts a public task ID. Only legacy rows may be
	// looked up by their provider task ID; new rows require their state ID.
	GetByProviderTask(ctx context.Context, taskID string, userID, apiKeyID int64) (*SeedanceVideoPendingBilling, error)
	ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*SeedanceVideoPendingBilling, error)
	ClaimSettlement(ctx context.Context, taskID string, userID, apiKeyID int64, lease time.Duration) (bool, error)
	Reschedule(ctx context.Context, stateID, upstreamStatus string, dueAt time.Time, lastError string) error
	MarkSettled(ctx context.Context, stateID string, actualCost float64) error
	MarkReleased(ctx context.Context, stateID string) error
}

// Optional extensions retain compatibility with existing test doubles while
// production freezes execution settings and supports controlled review.
type SeedanceVideoTaskExecutionRepository interface {
	AssignAccountWithExecution(ctx context.Context, stateID string, accountID int64, providerID string, accountCost *SeedanceAccountCostSnapshot, execution *VideoTasksExecutionSnapshot, expectedAPIKey string) error
}

type SeedanceVideoTaskAccountDependencyRepository interface {
	HasUnfinishedForAccount(ctx context.Context, accountID int64) (bool, error)
}

type SeedanceVideoTaskReviewRepository interface {
	MarkNeedsReview(ctx context.Context, stateID, reason string, deadline time.Time) error
	MarkNeedsReviewWithLease(ctx context.Context, stateID, reason string, deadline, leaseUntil time.Time) error
	ListNeedingReview(ctx context.Context, limit, offset int) ([]*SeedanceVideoPendingBilling, error)
	ClaimReviewResolution(ctx context.Context, stateID string, lease time.Duration) (bool, error)
	GetByStateID(ctx context.Context, stateID string) (*SeedanceVideoPendingBilling, error)
	RecordReviewDecisionWithLease(ctx context.Context, stateID string, decision VideoTaskReviewDecision, leaseUntil time.Time) error
	ResumeReviewPollingWithLease(ctx context.Context, stateID string, leaseUntil time.Time) error
}

type VideoTaskReviewDecision struct {
	ActorID   int64     `json:"actor_id"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason"`
	DecidedAt time.Time `json:"decided_at"`
}

func (s *OpenAIGatewayService) MarkSeedanceVideoTaskNeedsReview(ctx context.Context, pending *SeedanceVideoPendingBilling, reason string, deadline time.Time) error {
	if s == nil || pending == nil {
		return errors.New("video task review state is unavailable")
	}
	repo, ok := s.seedanceVideoTaskRepo.(SeedanceVideoTaskReviewRepository)
	if !ok {
		return errors.New("video task review repository is unavailable")
	}
	var err error
	if pending.LeaseUntil != nil {
		err = repo.MarkNeedsReviewWithLease(ctx, pending.StateID, reason, deadline, *pending.LeaseUntil)
	} else {
		err = repo.MarkNeedsReview(ctx, pending.StateID, reason, deadline)
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	pending.SettlementStatus, pending.ReviewReason = SeedanceVideoSettlementNeedsReview, reason
	pending.ReviewDeadline, pending.ReviewRequestedAt = &deadline, &now
	pending.LeaseUntil = nil
	return nil
}

// SeedanceVideoTaskCancellationRepository is an optional extension for
// repositories that can claim a cancellation before the upstream DELETE is
// sent. Keeping it separate from the base interface avoids breaking existing
// lightweight repositories and test doubles that only implement settlement.
type SeedanceVideoTaskCancellationRepository interface {
	ClaimCancellation(ctx context.Context, taskID string, userID, apiKeyID int64, lease time.Duration) (bool, error)
}

// SeedanceVideoTaskReleaseStatusRepository is an optional extension used by
// the production repository to persist the terminal provider-neutral status
// together with the accounting release. Keeping it separate preserves
// compatibility with lightweight repository implementations and older tests.
type SeedanceVideoTaskReleaseStatusRepository interface {
	MarkReleasedWithStatus(ctx context.Context, stateID, upstreamStatus string) error
}

// The lease-aware mutation interfaces are optional extensions.  Keeping each
// operation separate lets small in-memory repositories and older integrations
// continue to implement the base contract while the production repository can
// reject writes made by a worker that has lost its database lease.
type SeedanceVideoTaskRescheduleLeaseRepository interface {
	RescheduleWithLease(ctx context.Context, stateID, upstreamStatus string, dueAt time.Time, lastError string, leaseUntil time.Time) error
}

type SeedanceVideoTaskSettlementLeaseRepository interface {
	MarkSettledWithLease(ctx context.Context, stateID string, actualCost float64, leaseUntil time.Time) error
}

type SeedanceVideoTaskReleaseLeaseRepository interface {
	MarkReleasedWithLease(ctx context.Context, stateID string, leaseUntil time.Time) error
}

type SeedanceVideoTaskReleaseStatusLeaseRepository interface {
	MarkReleasedWithStatusWithLease(ctx context.Context, stateID, upstreamStatus string, leaseUntil time.Time) error
}

// SeedanceVideoTaskReleaseIntentLeaseRepository persists a terminal provider
// observation while retaining the processing lease.  It is deliberately
// separate from the released mutation: the balance operation can be retried
// after this intent is durable without allowing a later recovery pass to poll
// and settle the task again.
type SeedanceVideoTaskReleaseIntentLeaseRepository interface {
	MarkReleaseIntentWithLease(ctx context.Context, stateID, upstreamStatus string, leaseUntil time.Time) error
}

func (s *OpenAIGatewayService) SetSeedanceVideoTaskRepository(repo SeedanceVideoTaskRepository) {
	if s != nil {
		s.seedanceVideoTaskRepo = repo
	}
}
