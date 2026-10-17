package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *seedanceVideoTaskRepository) markNeedsReview(ctx context.Context, stateID, reason string, deadline time.Time, leaseUntil *time.Time) error {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" || strings.TrimSpace(reason) == "" || len(reason) > 2000 || deadline.IsZero() {
		return errors.New("video task review request is invalid")
	}
	query := `UPDATE custom_seedance_video_tasks
SET settlement_status = 'needs_review', review_reason = $2,
    review_requested_at = COALESCE(review_requested_at, NOW()), review_deadline = $3,
    lease_until = NULL, updated_at = NOW()
WHERE state_id = $1 AND settlement_status = 'pending'`
	args := []any{stateID, strings.TrimSpace(reason), deadline}
	if leaseUntil != nil {
		if leaseUntil.IsZero() {
			return errors.New("video task review lease is invalid")
		}
		query = strings.TrimSuffix(query, "settlement_status = 'pending'") +
			`settlement_status = 'processing' AND lease_until = $4 AND lease_until > NOW()`
		args = append(args, *leaseUntil)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	return seedanceVideoTaskMutationResult(result, err)
}

func (r *seedanceVideoTaskRepository) MarkNeedsReview(ctx context.Context, stateID, reason string, deadline time.Time) error {
	return r.markNeedsReview(ctx, stateID, reason, deadline, nil)
}

func (r *seedanceVideoTaskRepository) MarkNeedsReviewWithLease(ctx context.Context, stateID, reason string, deadline, leaseUntil time.Time) error {
	return r.markNeedsReview(ctx, stateID, reason, deadline, &leaseUntil)
}

func (r *seedanceVideoTaskRepository) ListNeedingReview(ctx context.Context, limit, offset int) ([]*service.SeedanceVideoPendingBilling, error) {
	if r == nil || r.db == nil || offset < 0 {
		return nil, errors.New("video task review listing is invalid")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+seedanceVideoTaskSelectColumns+`
FROM custom_seedance_video_tasks
WHERE settlement_status = 'needs_review'
   OR (settlement_status = 'processing' AND review_reason <> '' AND lease_until <= NOW())
ORDER BY review_deadline, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]*service.SeedanceVideoPendingBilling, 0, limit)
	for rows.Next() {
		task, err := scanSeedanceVideoTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (r *seedanceVideoTaskRepository) ClaimReviewResolution(ctx context.Context, stateID string, lease time.Duration) (bool, error) {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" {
		return false, errors.New("video task review claim is invalid")
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	result, err := r.db.ExecContext(ctx, `UPDATE custom_seedance_video_tasks
SET settlement_status = 'processing', lease_until = $2, updated_at = NOW()
WHERE state_id = $1 AND (settlement_status = 'needs_review'
  OR (settlement_status = 'processing' AND review_reason <> '' AND lease_until <= NOW()))`, stateID, time.Now().Add(lease))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (r *seedanceVideoTaskRepository) HasUnfinishedForAccount(ctx context.Context, accountID int64) (bool, error) {
	if r == nil || r.db == nil || accountID <= 0 {
		return false, errors.New("video task account dependency lookup is invalid")
	}
	return querySeedanceVideoTaskAccountDependencies(ctx, r.db, accountID)
}

func (r *seedanceVideoTaskRepository) GetByStateID(ctx context.Context, stateID string) (*service.SeedanceVideoPendingBilling, error) {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" {
		return nil, service.ErrSeedanceVideoTaskNotFound
	}
	return scanSeedanceVideoTask(r.db.QueryRowContext(ctx, `SELECT `+seedanceVideoTaskSelectColumns+`
FROM custom_seedance_video_tasks WHERE state_id = $1`, stateID))
}

func (r *seedanceVideoTaskRepository) RecordReviewDecisionWithLease(ctx context.Context, stateID string, decision service.VideoTaskReviewDecision, leaseUntil time.Time) error {
	if decision.ActorID <= 0 || strings.TrimSpace(decision.Reason) == "" || len(decision.Reason) > 2000 || decision.DecidedAt.IsZero() || leaseUntil.IsZero() {
		return errors.New("video task review decision is invalid")
	}
	switch decision.Action {
	case "retry_poll", "confirmed_failed", "confirmed_canceled":
	default:
		return errors.New("unsupported video task review decision")
	}
	if r == nil || r.db == nil {
		return errors.New("video task repository is unavailable")
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE custom_seedance_video_tasks
SET review_decisions = review_decisions || jsonb_build_array($2::jsonb), updated_at = NOW()
WHERE state_id = $1 AND settlement_status = 'processing'
  AND lease_until = $3 AND lease_until > NOW()`, stateID, string(encoded), leaseUntil)
	return seedanceVideoTaskMutationResult(result, err)
}

func (r *seedanceVideoTaskRepository) ResumeReviewPollingWithLease(ctx context.Context, stateID string, leaseUntil time.Time) error {
	if r == nil || r.db == nil || leaseUntil.IsZero() {
		return errors.New("video task review lease is unavailable")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE custom_seedance_video_tasks
SET settlement_status = 'pending', review_reason = '', next_poll_at = NOW(), lease_until = NULL, updated_at = NOW()
WHERE state_id = $1 AND settlement_status = 'processing' AND provider_task_id IS NOT NULL
  AND lease_until = $2 AND lease_until > NOW()
  AND LOWER(TRIM(upstream_status)) NOT IN ('failed', 'error', 'canceled', 'cancelled')`, stateID, leaseUntil)
	return seedanceVideoTaskMutationResult(result, err)
}

func querySeedanceVideoTaskAccountDependencies(ctx context.Context, db sqlExecutor, accountID int64) (bool, error) {
	var exists bool
	rows, err := db.QueryContext(ctx, `SELECT EXISTS (
    SELECT 1 FROM custom_seedance_video_tasks
    WHERE account_id = $1 AND (settlement_status IN ('pending', 'processing', 'needs_review')
        OR (settlement_status = 'settled' AND COALESCE(settled_at, created_at) >= $2))
)`, accountID, time.Now().Add(-service.SeedanceVideoTaskCredentialRetention))
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, rows.Err()
	}
	if err := rows.Scan(&exists); err != nil {
		return false, err
	}
	return exists, rows.Err()
}
