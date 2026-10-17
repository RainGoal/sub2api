//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestVideoTaskMeteringReadPreservesNullAndDatabaseErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
		err  error
	}{
		{name: "not_yet_frozen"},
		{name: "frozen", data: []byte(`{"version":1,"output_seconds":8.5}`)},
		{name: "missing_task", err: sql.ErrNoRows},
		{name: "database_unavailable", err: errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			query := mock.ExpectQuery(regexp.QuoteMeta("SELECT metering_snapshot FROM custom_seedance_video_tasks\nWHERE state_id = $1 AND task_id_version = 1")).WithArgs("vid_task")
			if tc.err != nil {
				query.WillReturnError(tc.err)
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"metering_snapshot"}).AddRow(tc.data))
			}
			repo := NewSeedanceVideoTaskRepository(db).(service.VideoTaskMeteringRepository)
			payload, err := repo.GetVideoTaskMetering(context.Background(), "vid_task")
			switch {
			case errors.Is(tc.err, sql.ErrNoRows):
				require.ErrorIs(t, err, service.ErrSeedanceVideoTaskNotFound)
			case tc.err != nil:
				require.ErrorIs(t, err, tc.err)
			case tc.data == nil:
				require.NoError(t, err)
				require.Nil(t, payload)
			default:
				require.NoError(t, err)
				require.JSONEq(t, string(tc.data.([]byte)), string(payload))
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
