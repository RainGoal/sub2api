package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type modelPlazaMonitorRepository struct{ db *sql.DB }

func NewModelPlazaMonitorRepository(db *sql.DB) service.ModelPlazaMonitorRepository {
	return &modelPlazaMonitorRepository{db: db}
}

func (r *modelPlazaMonitorRepository) SaveConfig(ctx context.Context, cfg service.ModelPlazaMonitorConfig, targets []service.ModelPlazaMonitorTarget) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// The settings row serializes concurrent saves, including the first save.
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW()) ON CONFLICT (key) DO NOTHING`, service.SettingKeyModelPlazaMonitor, `{"version":0,"enabled":false,"default_interval_seconds":300,"groups":[]}`); err != nil {
		return err
	}
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, service.SettingKeyModelPlazaMonitor).Scan(&previous); err != nil {
		return err
	}
	var current service.ModelPlazaMonitorConfig
	if err := json.Unmarshal([]byte(previous), &current); err != nil {
		return err
	}
	if current.Version != cfg.Version {
		return service.ErrModelPlazaMonitorConflict
	}
	cfg.Version++
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET value=$2,updated_at=NOW() WHERE key=$1`, service.SettingKeyModelPlazaMonitor, string(data)); err != nil {
		return err
	}
	ids := make([]int64, 0, len(targets))
	for _, target := range targets {
		var id int64
		err := tx.QueryRowContext(ctx, `INSERT INTO model_plaza_monitor_targets AS t
 (group_id,platform,model,api_mode,api_key_id,signature,interval_seconds)
 VALUES ($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT (group_id,platform,model) DO UPDATE SET
 api_mode=EXCLUDED.api_mode,api_key_id=EXCLUDED.api_key_id,interval_seconds=EXCLUDED.interval_seconds,
 status=CASE WHEN t.signature=EXCLUDED.signature THEN t.status ELSE 'unknown' END,
 failures=CASE WHEN t.signature=EXCLUDED.signature THEN t.failures ELSE 0 END,
 last_checked_at=CASE WHEN t.signature=EXCLUDED.signature THEN t.last_checked_at ELSE NULL END,
 next_check_at=CASE WHEN t.signature=EXCLUDED.signature THEN t.next_check_at ELSE NOW() END,
 lease_token=CASE WHEN t.signature=EXCLUDED.signature THEN t.lease_token ELSE NULL END,
 lease_until=CASE WHEN t.signature=EXCLUDED.signature THEN t.lease_until ELSE NULL END,
 signature=EXCLUDED.signature RETURNING id`, target.GroupID, target.Platform, target.Model, target.APIMode, target.APIKeyID, target.Signature, target.IntervalSeconds).Scan(&id)
		if err != nil {
			return err
		}
		ids = append(ids, id)
		// Only the changed target loses its old history. Unchanged targets keep
		// their cadence and do not incur an extra charge on an unrelated save.
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_plaza_monitor_checks WHERE target_id=$1 AND EXISTS (SELECT 1 FROM model_plaza_monitor_targets WHERE id=$1 AND last_checked_at IS NULL)`, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_plaza_monitor_targets WHERE NOT (id=ANY($1::bigint[]))`, pq.Array(ids)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *modelPlazaMonitorRepository) ListStates(ctx context.Context, groupID int64) ([]service.ModelPlazaMonitorState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,group_id,platform,model,api_mode,api_key_id,signature,interval_seconds,failures,status,last_checked_at,next_check_at FROM model_plaza_monitor_targets WHERE ($1::bigint=0 OR group_id=$1) ORDER BY next_check_at,id`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.ModelPlazaMonitorState{}
	for rows.Next() {
		var state service.ModelPlazaMonitorState
		if err := rows.Scan(&state.ID, &state.Target.GroupID, &state.Target.Platform, &state.Target.Model, &state.Target.APIMode, &state.Target.APIKeyID, &state.Target.Signature, &state.Target.IntervalSeconds, &state.Failures, &state.Status, &state.LastCheckedAt, &state.NextCheckAt); err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, rows.Err()
}

func (r *modelPlazaMonitorRepository) Claim(ctx context.Context, target service.ModelPlazaMonitorTarget, now time.Time, token string) (*service.ModelPlazaMonitorState, error) {
	state := &service.ModelPlazaMonitorState{Target: target, LeaseToken: token}
	err := r.db.QueryRowContext(ctx, `UPDATE model_plaza_monitor_targets SET lease_token=$1,lease_until=$2 WHERE group_id=$3 AND platform=$4 AND model=$5 AND signature=$6 AND next_check_at<=$7 AND (lease_until IS NULL OR lease_until<$7) RETURNING id,failures,status,last_checked_at,next_check_at`, token, now.Add(time.Minute), target.GroupID, target.Platform, target.Model, target.Signature, now).Scan(&state.ID, &state.Failures, &state.Status, &state.LastCheckedAt, &state.NextCheckAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (r *modelPlazaMonitorRepository) Complete(ctx context.Context, state service.ModelPlazaMonitorState, result service.ModelPlazaMonitorCheck) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var failures int
	// An expired lease, a config edit or deletion invalidates the old worker.
	err = tx.QueryRowContext(ctx, `SELECT failures FROM model_plaza_monitor_targets WHERE id=$1 AND signature=$2 AND lease_token=$3 AND lease_until>NOW() FOR UPDATE`, state.ID, state.Target.Signature, state.LeaseToken).Scan(&failures)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if result.ServiceFailure {
		failures++
		result.Status = "degraded"
		if failures >= 2 {
			result.Status = "unavailable"
		}
	} else {
		failures = 0
	}
	if result.Status != "operational" && result.Status != "degraded" && result.Status != "unavailable" && result.Status != "unknown" {
		return fmt.Errorf("invalid model plaza monitor result status")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE model_plaza_monitor_targets SET status=$2,failures=$3,last_checked_at=$4,next_check_at=$5,lease_until=NULL,lease_token=NULL WHERE id=$1`, state.ID, result.Status, failures, result.CheckedAt, result.CheckedAt.Add(time.Duration(state.Target.IntervalSeconds)*time.Second)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO model_plaza_monitor_checks (target_id,status,checked_at,latency_ms,reason) VALUES ($1,$2,$3,$4,$5)`, state.ID, result.Status, result.CheckedAt, result.LatencyMs, result.Reason); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM model_plaza_monitor_checks WHERE target_id=$1 AND checked_at<$2`, state.ID, result.CheckedAt.Add(-24*time.Hour)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *modelPlazaMonitorRepository) History(ctx context.Context, stateID int64, limit int) ([]service.ModelPlazaMonitorPoint, error) {
	if limit < 1 || limit > 100 {
		limit = 18
	}
	rows, err := r.db.QueryContext(ctx, `SELECT status,checked_at,latency_ms FROM model_plaza_monitor_checks WHERE target_id=$1 AND checked_at>NOW()-INTERVAL '24 hours' ORDER BY checked_at DESC,id DESC LIMIT $2`, stateID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.ModelPlazaMonitorPoint{}
	for rows.Next() {
		var point service.ModelPlazaMonitorPoint
		if err := rows.Scan(&point.Status, &point.CheckedAt, &point.LatencyMs); err != nil {
			return nil, err
		}
		out = append(out, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
