package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrVideoTaskReviewUnavailable = infraerrors.New(http.StatusServiceUnavailable, "VIDEO_TASK_REVIEW_UNAVAILABLE", "Video task review is unavailable")
	ErrVideoTaskReviewInvalid     = infraerrors.New(http.StatusUnprocessableEntity, "VIDEO_TASK_REVIEW_INVALID", "A supported decision and a reason are required")
	ErrVideoTaskReviewConflict    = infraerrors.New(http.StatusConflict, "VIDEO_TASK_REVIEW_CONFLICT", "Task cannot accept this review decision")
	ErrVideoTaskReviewNotFound    = infraerrors.New(http.StatusNotFound, "VIDEO_TASK_REVIEW_NOT_FOUND", "Video task not found")
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

func (s *OpenAIGatewayService) ResolveSeedanceVideoTaskReview(ctx context.Context, stateID string, actorID int64, action, reason string) (*SeedanceVideoPendingBilling, error) {
	stateID, action, reason = strings.TrimSpace(stateID), strings.TrimSpace(action), strings.TrimSpace(reason)
	if actorID <= 0 || stateID == "" || len(stateID) > 128 || reason == "" || len(reason) > 2000 {
		return nil, ErrVideoTaskReviewInvalid
	}
	switch action {
	case "retry_poll", "confirmed_failed", "confirmed_canceled":
	default:
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
	if pending.SettlementStatus == SeedanceVideoSettlementReleased {
		if (action == "confirmed_failed" && NormalizeSeedanceVideoReleaseStatus(pending.UpstreamStatus) == SeedanceVideoResponseStatusFailed) ||
			(action == "confirmed_canceled" && NormalizeSeedanceVideoReleaseStatus(pending.UpstreamStatus) == SeedanceVideoResponseStatusCanceled) {
			return pending, nil
		}
		return nil, ErrVideoTaskReviewConflict
	}
	if action == "retry_poll" && (pending.UpstreamID() == "" || IsSeedanceVideoReleaseIntentStatus(pending.UpstreamStatus)) {
		return nil, ErrVideoTaskReviewConflict
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
	decision := VideoTaskReviewDecision{ActorID: actorID, Action: action, Reason: reason, DecidedAt: time.Now().UTC()}
	if err := repo.RecordReviewDecisionWithLease(ctx, stateID, decision, *pending.LeaseUntil); err != nil {
		return nil, err
	}
	if action == "retry_poll" {
		if err := repo.ResumeReviewPollingWithLease(ctx, stateID, *pending.LeaseUntil); err != nil {
			return nil, err
		}
	} else {
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
