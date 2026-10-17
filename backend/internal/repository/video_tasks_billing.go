package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Every v1 video capture/release locks this same row before dedup or money
// changes. Image jobs and legacy video tasks never enter this path.
func lockVideoTaskBilling(ctx context.Context, tx *sql.Tx, stateID string, userID, apiKeyID int64, holdID string, holdAmount float64, lease *time.Time, allowUnbound bool) (sql.NullFloat64, error) {
	var status string
	var providerID sql.NullString
	var storedLease sql.NullTime
	var actualCost sql.NullFloat64
	err := tx.QueryRowContext(ctx, `SELECT settlement_status, provider_task_id, lease_until, actual_cost
FROM custom_seedance_video_tasks
WHERE state_id = $1 AND task_id_version = 1 AND user_id = $2 AND api_key_id = $3
  AND hold_id = $4 AND hold_amount = $5
FOR UPDATE`, stateID, userID, apiKeyID, holdID, service.QuantizeUsageBillingAmount(holdAmount)).Scan(&status, &providerID, &storedLease, &actualCost)
	if errors.Is(err, sql.ErrNoRows) {
		return actualCost, service.ErrSeedanceVideoTaskNotFound
	}
	if err != nil {
		return actualCost, err
	}
	if allowUnbound && lease == nil && status == service.SeedanceVideoSettlementPending &&
		!storedLease.Valid && (!providerID.Valid || providerID.String == "") {
		return actualCost, nil
	}
	if lease == nil || !storedLease.Valid || !storedLease.Time.Equal(*lease) || status != service.SeedanceVideoSettlementProcessing {
		return actualCost, service.ErrSeedanceVideoLeaseMutationUnsupported
	}
	// Read the database clock after acquiring the lock, including after waiting
	// behind another transaction; NOW() is fixed at transaction start.
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return actualCost, err
	}
	if !storedLease.Time.After(now) {
		return actualCost, service.ErrSeedanceVideoLeaseMutationUnsupported
	}
	return actualCost, nil
}

func videoTaskBillingRequestExists(ctx context.Context, tx *sql.Tx, requestID string, apiKeyID int64) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
SELECT 1 FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2
UNION ALL
SELECT 1 FROM usage_billing_dedup_archive WHERE request_id = $1 AND api_key_id = $2
)`, requestID, apiKeyID).Scan(&exists)
	return exists, err
}

func lockVideoTaskCapture(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	if cmd.RequestID != service.StableSeedanceVideoBillingRequestID(cmd.VideoTaskStateID) {
		return service.ErrUsageBillingRequestConflict
	}
	if _, err := lockVideoTaskBilling(ctx, tx, cmd.VideoTaskStateID, cmd.UserID, cmd.APIKeyID,
		cmd.BalanceHoldID, cmd.BalanceHoldAmount, cmd.VideoTaskLeaseUntil, false); err != nil {
		return err
	}
	released, err := videoTaskBillingRequestExists(ctx, tx, service.BatchImageReleaseRequestID(cmd.BalanceHoldID), cmd.APIKeyID)
	if err != nil {
		return err
	}
	if released {
		return service.ErrVideoTaskAlreadyReleased
	}
	return nil
}

func (r *usageBillingRepository) releaseVideoTaskBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	cmd.Normalize()
	if cmd.RequestID != service.BatchImageReleaseRequestID(cmd.BatchID) {
		return nil, service.ErrUsageBillingRequestConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	actualCost, err := lockVideoTaskBilling(ctx, tx, cmd.VideoTaskStateID, cmd.UserID, cmd.APIKeyID,
		cmd.BatchID, cmd.HoldAmount, cmd.VideoTaskLeaseUntil, true)
	if err != nil {
		return nil, err
	}
	captured, err := videoTaskBillingRequestExists(ctx, tx, service.StableSeedanceVideoBillingRequestID(cmd.VideoTaskStateID), cmd.APIKeyID)
	if err != nil {
		return nil, err
	}
	if captured {
		return reconcileCapturedVideoTask(ctx, tx, cmd.VideoTaskStateID, actualCost)
	}
	claimed, err := r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return &service.BatchImageBalanceHoldResult{Applied: false}, nil
	}
	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	result.Applied = true
	return result, nil
}

func reconcileCapturedVideoTask(ctx context.Context, tx *sql.Tx, stateID string, actualCost sql.NullFloat64) (*service.BatchImageBalanceHoldResult, error) {
	if !actualCost.Valid || actualCost.Float64 < 0 || math.IsNaN(actualCost.Float64) || math.IsInf(actualCost.Float64, 0) {
		return nil, errors.New("captured video task has no durable paid amount")
	}
	// actual_cost was committed with the successful capture dedup entry. Never
	// use a later provider response or the release command to reconstruct it.
	_, err := tx.ExecContext(ctx, `UPDATE custom_seedance_video_tasks
SET settlement_status = 'settled', upstream_status = 'completed', lease_until = NULL,
    review_reason = '', settled_at = COALESCE(settled_at, NOW()), updated_at = NOW()
WHERE state_id = $1`, stateID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, service.ErrVideoTaskAlreadyCaptured
}
