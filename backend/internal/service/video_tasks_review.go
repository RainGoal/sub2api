package service

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrVideoTaskReviewUnavailable     = infraerrors.New(http.StatusServiceUnavailable, "VIDEO_TASK_REVIEW_UNAVAILABLE", "Video task review is unavailable")
	ErrVideoTaskReviewInvalid         = infraerrors.New(http.StatusUnprocessableEntity, "VIDEO_TASK_REVIEW_INVALID", "A supported decision and a reason are required")
	ErrVideoTaskReviewConflict        = infraerrors.New(http.StatusConflict, "VIDEO_TASK_REVIEW_CONFLICT", "Task cannot accept this review decision")
	ErrVideoTaskReviewNotFound        = infraerrors.New(http.StatusNotFound, "VIDEO_TASK_REVIEW_NOT_FOUND", "Video task not found")
	ErrVideoTaskReviewMeteringInvalid = infraerrors.New(http.StatusUnprocessableEntity, "VIDEO_TASK_REVIEW_METERING_INVALID", "Verified metering must satisfy the task's frozen billing terms")
)

func (s *OpenAIGatewayService) ListSeedanceVideoTaskReviews(ctx context.Context, limit, offset int) ([]*SeedanceVideoPendingBilling, error) {
	if s == nil {
		return nil, ErrVideoTaskReviewUnavailable
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, ErrVideoTaskReviewInvalid
	}
	repo, ok := s.seedanceVideoTaskRepo.(SeedanceVideoTaskReviewRepository)
	if !ok {
		return nil, ErrVideoTaskReviewUnavailable
	}
	return repo.ListNeedingReview(ctx, limit, offset)
}

func videoTaskReviewCompletionResult(metering *VideoTaskReviewMetering) *OpenAIForwardResult {
	return &OpenAIForwardResult{VideoCount: 1,
		VideoDurationSeconds: int(metering.OutputSeconds), VideoReferenceInputSeconds: int(metering.ReferenceSeconds),
		VideoDurationSecondsExact: metering.OutputSeconds, VideoReferenceInputSecondsExact: metering.ReferenceSeconds}
}

func (s *OpenAIGatewayService) validateVideoTaskReviewMetering(ctx context.Context, pending *SeedanceVideoPendingBilling, metering *VideoTaskReviewMetering) error {
	// Legacy prices use integer seconds. Never silently truncate an operator's
	// quantities or substitute the requested duration for missing actual output.
	if pending.SalesSnapshot == nil && (metering.OutputSeconds <= 0 || math.Trunc(metering.OutputSeconds) != metering.OutputSeconds || math.Trunc(metering.ReferenceSeconds) != metering.ReferenceSeconds) {
		return ErrVideoTaskReviewMeteringInvalid
	}
	verified := *pending
	verified.ReviewDecisions = append(append([]VideoTaskReviewDecision(nil), pending.ReviewDecisions...),
		VideoTaskReviewDecision{Action: "confirmed_succeeded", Metering: metering})
	result, cost, err := s.BuildSeedanceVideoCompletionBillingWithMetering(ctx, &verified, videoTaskReviewCompletionResult(metering))
	if errors.Is(err, ErrVideoTaskMeteringUnavailable) {
		return err
	}
	if err != nil || result.VideoDurationSecondsExact != metering.OutputSeconds || result.VideoReferenceInputSecondsExact != metering.ReferenceSeconds {
		return ErrVideoTaskReviewMeteringInvalid
	}
	if !validSeedanceAccountCost(cost.ActualCost) || (pending.HoldAmount > 0 && QuantizeUsageBillingAmount(cost.ActualCost) > pending.HoldAmount) {
		return ErrVideoTaskReviewMeteringInvalid
	}
	if err := ApplySeedanceAccountCost(&OpenAIRecordUsageInput{Result: result, CostOverride: cost}, &verified); err != nil {
		return ErrVideoTaskReviewMeteringInvalid
	}
	return nil
}

func (s *OpenAIGatewayService) ResolveSeedanceVideoTaskReview(ctx context.Context, stateID string, actorID int64, action, reason string, metering ...*VideoTaskReviewMetering) (*SeedanceVideoPendingBilling, error) {
	stateID, action, reason = strings.TrimSpace(stateID), strings.TrimSpace(action), strings.TrimSpace(reason)
	if actorID <= 0 || stateID == "" || len(stateID) > 128 || reason == "" || len(reason) > 2000 {
		return nil, ErrVideoTaskReviewInvalid
	}
	switch action {
	case "retry_poll", "confirmed_failed", "confirmed_canceled", "confirmed_succeeded":
	default:
		return nil, ErrVideoTaskReviewInvalid
	}
	var verified *VideoTaskReviewMetering
	if len(metering) > 1 {
		return nil, ErrVideoTaskReviewInvalid
	}
	if len(metering) == 1 {
		verified = metering[0]
	}
	if action == "confirmed_succeeded" {
		if verified == nil || !validSeedanceAccountCost(verified.OutputSeconds) || !validSeedanceAccountCost(verified.ReferenceSeconds) ||
			verified.OutputSeconds > math.MaxInt32 || verified.ReferenceSeconds > math.MaxInt32 {
			return nil, ErrVideoTaskReviewMeteringInvalid
		}
	} else if verified != nil {
		return nil, ErrVideoTaskReviewInvalid
	}
	if s == nil {
		return nil, ErrVideoTaskReviewUnavailable
	}
	repo, ok := s.seedanceVideoTaskRepo.(SeedanceVideoTaskReviewRepository)
	if !ok {
		return nil, ErrVideoTaskReviewUnavailable
	}
	pending, err := repo.GetByStateID(ctx, stateID)
	if errors.Is(err, ErrSeedanceVideoTaskNotFound) {
		return nil, ErrVideoTaskReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	if existing := pending.ConfirmedReviewMetering(); existing != nil {
		if action == "confirmed_succeeded" && *existing == *verified && pending.SettlementStatus != SeedanceVideoSettlementReleased {
			return pending, nil
		}
		return nil, ErrVideoTaskReviewConflict
	}
	if pending.SettlementStatus == SeedanceVideoSettlementReleased {
		if (action == "confirmed_failed" && NormalizeSeedanceVideoReleaseStatus(pending.UpstreamStatus) == SeedanceVideoResponseStatusFailed) ||
			(action == "confirmed_canceled" && NormalizeSeedanceVideoReleaseStatus(pending.UpstreamStatus) == SeedanceVideoResponseStatusCanceled) {
			return pending, nil
		}
		return nil, ErrVideoTaskReviewConflict
	}
	if (action == "retry_poll" || action == "confirmed_succeeded") && (pending.UpstreamID() == "" || IsSeedanceVideoReleaseIntentStatus(pending.UpstreamStatus)) {
		return nil, ErrVideoTaskReviewConflict
	}
	if action == "confirmed_succeeded" {
		if strings.EqualFold(strings.TrimSpace(pending.UpstreamStatus), SeedanceVideoCancellationRequestedStatus) {
			return nil, ErrVideoTaskReviewConflict
		}
		if err := s.validateVideoTaskReviewMetering(ctx, pending, verified); err != nil {
			return nil, err
		}
	}
	claimed, err := repo.ClaimReviewResolution(ctx, stateID, seedanceVideoTaskLease)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, ErrVideoTaskReviewConflict
	}
	pending, err = repo.GetByStateID(ctx, stateID)
	if err != nil {
		return nil, err
	}
	if pending.LeaseUntil == nil {
		return nil, ErrVideoTaskReviewConflict
	}
	decision := VideoTaskReviewDecision{ActorID: actorID, Action: action, Reason: reason, DecidedAt: time.Now().UTC(), Metering: verified}
	if err := repo.RecordReviewDecisionWithLease(ctx, stateID, decision, *pending.LeaseUntil); err != nil {
		return nil, err
	}
	switch action {
	case "confirmed_succeeded":
		// The repository atomically records the verified quantities and queues
		// settlement. Recovery uses the existing idempotent accounting pipeline.
		return repo.GetByStateID(ctx, stateID)
	case "retry_poll":
		if err := repo.ResumeReviewPollingWithLease(ctx, stateID, *pending.LeaseUntil); err != nil {
			return nil, err
		}
	default:
		terminalStatus := SeedanceVideoResponseStatusFailed
		if action == "confirmed_canceled" {
			terminalStatus = SeedanceVideoResponseStatusCanceled
		}
		if err := s.PersistSeedanceVideoReleaseIntent(ctx, pending, terminalStatus); err != nil {
			return nil, err
		}
		if err := s.ReleaseSeedanceVideoBalance(ctx, pending); err != nil {
			if errors.Is(err, ErrVideoTaskAlreadyCaptured) {
				// The wallet transaction reconciled the already-captured task to
				// settled. Never overwrite that result with a release terminal.
				return repo.GetByStateID(ctx, stateID)
			}
			return nil, err
		}
		if err := s.ReleaseSeedanceVideoTaskWithStatus(ctx, pending, terminalStatus); err != nil {
			return nil, err
		}
	}
	return repo.GetByStateID(ctx, stateID)
}
