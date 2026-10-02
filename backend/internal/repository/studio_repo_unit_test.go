//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestStudioRepositoryScopesAllOperationsToOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewStudioRepository(db)
	task := service.StudioTask{ID: "task-1", Kind: "image", KeyID: 8, CreatedAt: 1, Status: "completed"}
	data, err := json.Marshal(task)
	require.NoError(t, err)
	rows := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"metadata"}).AddRow(data) }
	mock.ExpectQuery(`WHERE user_id = \$1 AND kind = \$2 AND task_id = \$3`).WithArgs(int64(7), "image", "task-1").WillReturnRows(rows())
	got, err := repo.Get(context.Background(), 7, "image", "task-1")
	require.NoError(t, err)
	require.Equal(t, task.ID, got.ID)
	mock.ExpectQuery(`WHERE user_id = \$1 ORDER BY created_at DESC, kind, task_id LIMIT \$2 OFFSET \$3`).WithArgs(int64(7), 101, 0).WillReturnRows(rows())
	items, err := repo.List(context.Background(), 7, 101, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	mock.ExpectQuery(`(?s)INSERT INTO custom_studio_tasks.*ON CONFLICT \(user_id, kind, task_id\).*WHERE custom_studio_tasks.api_key_id = EXCLUDED.api_key_id.*RETURNING metadata`).WithArgs(int64(7), "image", "task-1", int64(8), int64(1), string(data)).WillReturnRows(rows())
	_, err = repo.Upsert(context.Background(), 7, &task)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
