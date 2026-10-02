package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type studioRepository struct{ db *sql.DB }

func NewStudioRepository(db *sql.DB) service.StudioRepository { return &studioRepository{db: db} }

func (r *studioRepository) Get(ctx context.Context, userID int64, kind, id string) (*service.StudioTask, error) {
	var data []byte
	err := r.db.QueryRowContext(ctx, `SELECT metadata FROM custom_studio_tasks WHERE user_id = $1 AND kind = $2 AND task_id = $3`, userID, kind, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrStudioTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	var task service.StudioTask
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *studioRepository) List(ctx context.Context, userID int64, limit, offset int) ([]service.StudioTask, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT metadata FROM custom_studio_tasks WHERE user_id = $1 ORDER BY created_at DESC, kind, task_id LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]service.StudioTask, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var task service.StudioTask
		if err := json.Unmarshal(data, &task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// Upserts preserve immutable input snapshots even when two tabs register the
// same task simultaneously. A late poll cannot regress a terminal observation.
func (r *studioRepository) Upsert(ctx context.Context, userID int64, task *service.StudioTask) (*service.StudioTask, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}
	var saved []byte
	err = r.db.QueryRowContext(ctx, `
INSERT INTO custom_studio_tasks (user_id, kind, task_id, api_key_id, created_at, metadata)
VALUES ($1, $2, $3, $4, $5, $6::jsonb)
ON CONFLICT (user_id, kind, task_id) DO UPDATE SET
  metadata = custom_studio_tasks.metadata
    || jsonb_build_object('title', EXCLUDED.metadata->'title', 'project', EXCLUDED.metadata->'project', 'adopted', COALESCE(EXCLUDED.metadata->'adopted', '[]'::jsonb))
    || CASE WHEN custom_studio_tasks.metadata->>'status' IN ('completed', 'failed', 'canceled', 'cancelled')
         AND EXCLUDED.metadata->>'status' NOT IN ('completed', 'failed', 'canceled', 'cancelled')
       THEN '{}'::jsonb
       ELSE jsonb_build_object('status', EXCLUDED.metadata->'status',
         'imageResult', COALESCE(EXCLUDED.metadata->'imageResult', custom_studio_tasks.metadata->'imageResult'),
         'videoResult', COALESCE(EXCLUDED.metadata->'videoResult', custom_studio_tasks.metadata->'videoResult')) END,
  updated_at = NOW()
WHERE custom_studio_tasks.api_key_id = EXCLUDED.api_key_id
RETURNING metadata`, userID, task.Kind, task.ID, task.KeyID, task.CreatedAt, string(data)).Scan(&saved)
	if err != nil {
		return nil, err
	}
	var result service.StudioTask
	if err := json.Unmarshal(saved, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
