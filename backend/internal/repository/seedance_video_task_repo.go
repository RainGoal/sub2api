package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type seedanceVideoTaskRepository struct {
	db *sql.DB
}

func NewSeedanceVideoTaskRepository(db *sql.DB) service.SeedanceVideoTaskRepository {
	return &seedanceVideoTaskRepository{db: db}
}

const seedanceVideoTaskSelectColumns = `
state_id, COALESCE(provider_task_id, ''), user_id, api_key_id, group_id,
COALESCE(account_id, 0), provider_protocol, model, resolution, duration_seconds,
reference_video_count, original_model, request_payload_hash, hold_id,
hold_amount, total_cost_per_second, actual_cost_per_second, rate_multiplier,
is_subscription_billing, subscription_id, upstream_status, settlement_status,
retry_count, next_poll_at, lease_until, last_error_message, created_at`

type seedanceVideoTaskScanner interface {
	Scan(dest ...any) error
}

func scanSeedanceVideoTask(scanner seedanceVideoTaskScanner) (*service.SeedanceVideoPendingBilling, error) {
	var pending service.SeedanceVideoPendingBilling
	var groupID, subscriptionID sql.NullInt64
	var leaseUntil sql.NullTime
	var createdAt time.Time
	err := scanner.Scan(
		&pending.StateID, &pending.TaskID, &pending.UserID, &pending.APIKeyID, &groupID,
		&pending.AccountID, &pending.ProviderID, &pending.Model, &pending.Resolution, &pending.DurationSeconds,
		&pending.ReferenceVideoCount, &pending.OriginalModel, &pending.RequestPayloadHash,
		&pending.HoldID, &pending.HoldAmount, &pending.TotalCostPerSecond,
		&pending.ActualCostPerSecond, &pending.RateMultiplier, &pending.IsSubscriptionBilling,
		&subscriptionID, &pending.UpstreamStatus, &pending.SettlementStatus,
		&pending.RetryCount, &pending.NextPollAt, &leaseUntil, &pending.LastError,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSeedanceVideoTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if groupID.Valid {
		value := groupID.Int64
		pending.GroupID = &value
	}
	if subscriptionID.Valid {
		value := subscriptionID.Int64
		pending.SubscriptionID = &value
	}
	if leaseUntil.Valid {
		value := leaseUntil.Time
		pending.LeaseUntil = &value
	}
	pending.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	return &pending, nil
}

func (r *seedanceVideoTaskRepository) Create(ctx context.Context, pending *service.SeedanceVideoPendingBilling) error {
	if r == nil || r.db == nil {
		return errors.New("seedance video task repository db is nil")
	}
	if pending == nil || strings.TrimSpace(pending.StateID) == "" || strings.TrimSpace(pending.HoldID) == "" {
		return errors.New("seedance video task state is invalid")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(pending.CreatedAt))
	if err != nil {
		createdAt = time.Now().UTC()
	}
	nextPollAt := pending.NextPollAt
	if nextPollAt.IsZero() {
		nextPollAt = createdAt.Add(2 * time.Minute)
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO custom_seedance_video_tasks (
    state_id, user_id, api_key_id, group_id, account_id, provider_protocol, model, resolution,
    duration_seconds, reference_video_count, original_model, request_payload_hash,
    hold_id, hold_amount, total_cost_per_second, actual_cost_per_second,
    rate_multiplier, is_subscription_billing, subscription_id, upstream_status,
    settlement_status, next_poll_at, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, NULLIF($5, 0), $6, $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18, $19, 'creating', 'pending', $20, $21, $21
)`, pending.StateID, pending.UserID, pending.APIKeyID, pending.GroupID, pending.AccountID,
		pending.ProviderID, pending.Model, pending.Resolution, pending.DurationSeconds, pending.ReferenceVideoCount,
		pending.OriginalModel, pending.RequestPayloadHash, pending.HoldID, pending.HoldAmount,
		pending.TotalCostPerSecond, pending.ActualCostPerSecond, pending.RateMultiplier,
		pending.IsSubscriptionBilling, pending.SubscriptionID, nextPollAt, createdAt)
	if err != nil {
		return fmt.Errorf("create seedance video task: %w", err)
	}
	return nil
}

func (r *seedanceVideoTaskRepository) AssignAccount(ctx context.Context, stateID string, accountID int64, providerID string) error {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" || accountID <= 0 || strings.TrimSpace(providerID) == "" {
		return errors.New("seedance video task account assignment is invalid")
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE custom_seedance_video_tasks
SET account_id = $2, provider_protocol = $3, updated_at = NOW()
WHERE state_id = $1 AND settlement_status = 'pending' AND provider_task_id IS NULL`, stateID, accountID, providerID)
	return seedanceVideoTaskMutationResult(result, err)
}

func (r *seedanceVideoTaskRepository) BindProviderTask(ctx context.Context, stateID, taskID, upstreamStatus string, dueAt time.Time) error {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" || strings.TrimSpace(taskID) == "" {
		return errors.New("seedance provider task binding is invalid")
	}
	if strings.TrimSpace(upstreamStatus) == "" {
		upstreamStatus = "queued"
	}
	if dueAt.IsZero() {
		dueAt = time.Now()
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE custom_seedance_video_tasks
SET provider_task_id = $2, upstream_status = $3, next_poll_at = $4,
    lease_until = NULL, updated_at = NOW()
WHERE state_id = $1 AND settlement_status = 'pending'`, stateID, strings.TrimSpace(taskID),
		strings.ToLower(strings.TrimSpace(upstreamStatus)), dueAt)
	return seedanceVideoTaskMutationResult(result, err)
}

func (r *seedanceVideoTaskRepository) GetByProviderTask(ctx context.Context, taskID string, userID, apiKeyID int64) (*service.SeedanceVideoPendingBilling, error) {
	if r == nil || r.db == nil || strings.TrimSpace(taskID) == "" || userID <= 0 || apiKeyID <= 0 {
		return nil, service.ErrSeedanceVideoTaskNotFound
	}
	return scanSeedanceVideoTask(r.db.QueryRowContext(ctx, `SELECT `+seedanceVideoTaskSelectColumns+`
FROM custom_seedance_video_tasks
WHERE provider_task_id = $1 AND user_id = $2 AND api_key_id = $3
LIMIT 1`, strings.TrimSpace(taskID), userID, apiKeyID))
}

func (r *seedanceVideoTaskRepository) ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*service.SeedanceVideoPendingBilling, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("seedance video task repository db is nil")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
WITH due AS (
    SELECT id
    FROM custom_seedance_video_tasks
    WHERE (settlement_status = 'pending' AND next_poll_at <= $1)
       OR (settlement_status = 'processing' AND lease_until <= $1)
    ORDER BY next_poll_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $2
)
UPDATE custom_seedance_video_tasks AS task
SET settlement_status = 'processing', lease_until = $3, updated_at = $1
FROM due
WHERE task.id = due.id
RETURNING `+seedanceVideoTaskSelectColumns, now, limit, now.Add(lease))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]*service.SeedanceVideoPendingBilling, 0, limit)
	for rows.Next() {
		pending, scanErr := scanSeedanceVideoTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, pending)
	}
	return tasks, rows.Err()
}

func (r *seedanceVideoTaskRepository) ClaimSettlement(ctx context.Context, taskID string, userID, apiKeyID int64, lease time.Duration) (bool, error) {
	if r == nil || r.db == nil || strings.TrimSpace(taskID) == "" || userID <= 0 || apiKeyID <= 0 {
		return false, errors.New("seedance video settlement claim is invalid")
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE custom_seedance_video_tasks
SET settlement_status = 'processing', lease_until = $4, updated_at = NOW()
WHERE provider_task_id = $1 AND user_id = $2 AND api_key_id = $3
  AND (settlement_status = 'pending'
       OR (settlement_status = 'processing' AND lease_until <= NOW()))`,
		strings.TrimSpace(taskID), userID, apiKeyID, time.Now().Add(lease))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (r *seedanceVideoTaskRepository) Reschedule(ctx context.Context, stateID, upstreamStatus string, dueAt time.Time, lastError string) error {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" {
		return errors.New("seedance video task reschedule is invalid")
	}
	if dueAt.IsZero() {
		dueAt = time.Now()
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE custom_seedance_video_tasks
SET upstream_status = CASE WHEN $2 = '' THEN upstream_status ELSE $2 END,
    settlement_status = 'pending', next_poll_at = $3, lease_until = NULL,
    retry_count = retry_count + CASE WHEN $4 = '' THEN 0 ELSE 1 END,
    last_error_message = $4, updated_at = NOW()
WHERE state_id = $1 AND settlement_status NOT IN ('settled', 'released')`,
		stateID, strings.ToLower(strings.TrimSpace(upstreamStatus)), dueAt, strings.TrimSpace(lastError))
	return seedanceVideoTaskMutationResult(result, err)
}

func (r *seedanceVideoTaskRepository) MarkSettled(ctx context.Context, stateID string, actualCost float64) error {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" {
		return errors.New("seedance video task settlement is invalid")
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE custom_seedance_video_tasks
SET settlement_status = 'settled', upstream_status = 'completed', actual_cost = $2,
    lease_until = NULL, settled_at = NOW(), updated_at = NOW()
WHERE state_id = $1 AND settlement_status = 'processing'`, stateID, actualCost)
	return seedanceVideoTaskMutationResult(result, err)
}

func (r *seedanceVideoTaskRepository) MarkReleased(ctx context.Context, stateID string) error {
	if r == nil || r.db == nil || strings.TrimSpace(stateID) == "" {
		return errors.New("seedance video task release is invalid")
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE custom_seedance_video_tasks
SET settlement_status = 'released', lease_until = NULL, settled_at = NOW(), updated_at = NOW()
WHERE state_id = $1 AND settlement_status NOT IN ('settled', 'released')`, stateID)
	return seedanceVideoTaskMutationResult(result, err)
}

func seedanceVideoTaskMutationResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return rowsErr
	}
	if rows == 0 {
		return service.ErrSeedanceVideoTaskNotFound
	}
	return nil
}
