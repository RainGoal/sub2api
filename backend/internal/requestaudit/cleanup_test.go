package requestaudit

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCleanupSelectsDeletingBeforeIndexedExpiredBranch(t *testing.T) {
	for _, deleting := range []bool{true, false} {
		name := "newly expired"
		if deleting {
			name = "resume deleting"
		}
		t.Run(name, func(t *testing.T) {
			m, mock := mockManager(t)
			id := uuid.NewString()
			mock.ExpectBegin()
			expectRetention(mock)
			mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			lookup := mock.ExpectQuery(regexp.QuoteMeta(cleanupDeletingSQL))
			if deleting {
				lookup.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
			} else {
				lookup.WillReturnError(sql.ErrNoRows)
				mock.ExpectQuery(regexp.QuoteMeta(cleanupExpiredSQL)).WithArgs(3).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
			}
			mock.ExpectExec(`UPDATE request_audits SET cleanup_state='deleting'`).WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`DELETE FROM request_audit_payload_chunks`).WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 128))
			mock.ExpectExec(`DELETE FROM request_audits`).WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()
			more, err := m.cleanupBatch(context.Background())
			require.NoError(t, err)
			require.True(t, more)
		})
	}
}
