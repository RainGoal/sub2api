//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestVideoTaskAccountAssignmentLocksAndChecksSelectedCredential(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "rotated"}[changed], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := NewSeedanceVideoTaskRepository(db).(service.SeedanceVideoTaskExecutionRepository)
			execution := &service.VideoTasksExecutionSnapshot{Version: 1, Protocol: "bblabu_v1", BaseURL: "https://original.example/v1", UpstreamModel: "Seedance-2.0"}
			key := "original-key"
			if changed {
				key = "rotated-key"
			}
			mock.ExpectBegin()
			mock.ExpectQuery(`(?s)SELECT credentials FROM accounts.*deleted_at IS NULL.*FOR SHARE`).WithArgs(int64(20)).
				WillReturnRows(sqlmock.NewRows([]string{"credentials"}).AddRow([]byte(`{"api_key":"` + key + `","base_url":"https://original.example/v1","video_provider":"bblabu_v1"}`)))
			if changed {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*execution_snapshot = \$5.*provider_task_id IS NULL`).
					WithArgs("vid_00112233445566778899aabbccddeeff", int64(20), "bblabu_v1", nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err = repo.AssignAccountWithExecution(context.Background(), "vid_00112233445566778899aabbccddeeff", 20, "bblabu_v1", nil, execution, "original-key")
			if changed {
				require.ErrorContains(t, err, "identity changed")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoTaskAccountMutationLocksBeforeDependencyCheck(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)SELECT id, credentials, type FROM accounts.*ORDER BY id FOR UPDATE`).WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "type"}).AddRow(int64(20), []byte(`{"api_key":"original-key"}`), "apikey"))
	mock.ExpectQuery(`(?s)SELECT EXISTS.*needs_review.*settlement_status = 'settled'.*COALESCE\(settled_at, created_at\) >= \$2`).
		WithArgs(int64(20), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	err = guardVideoAccountMutation(context.Background(), db, []int64{20}, nil, map[string]any{"api_key": "replacement"}, false)
	require.ErrorIs(t, err, service.ErrVideoTaskCredentialsProtected)
	require.NoError(t, mock.ExpectationsWereMet())
}
