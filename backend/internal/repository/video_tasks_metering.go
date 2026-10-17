package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *seedanceVideoTaskRepository) GetVideoTaskMetering(ctx context.Context, stateID string) (json.RawMessage, error) {
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT metering_snapshot FROM custom_seedance_video_tasks
WHERE state_id = $1 AND task_id_version = 1`, stateID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSeedanceVideoTaskNotFound
	}
	return payload, err
}

func (r *seedanceVideoTaskRepository) SaveVideoTaskMeteringWithLease(ctx context.Context, stateID string, payload json.RawMessage, lease time.Time) error {
	if !json.Valid(payload) || lease.IsZero() {
		return fmt.Errorf("invalid video task metering snapshot")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE custom_seedance_video_tasks
SET metering_snapshot = $2::jsonb, updated_at = NOW()
WHERE state_id = $1 AND task_id_version = 1 AND settlement_status = 'processing'
AND lease_until = $3 AND lease_until > NOW()
AND (metering_snapshot IS NULL OR metering_snapshot = $2::jsonb)`, stateID, string(payload), lease)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return service.ErrSeedanceVideoTaskNotFound
	}
	return nil
}
