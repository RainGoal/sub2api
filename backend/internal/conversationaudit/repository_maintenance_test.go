package conversationaudit

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPartitionDayStrictlyValidatesNames(t *testing.T) {
	day, ok := partitionDay("conversation_audit_records_20260829")
	require.True(t, ok)
	require.Equal(t, "2026-08-29", day.Format("2006-01-02"))
	for _, invalid := range []string{
		"conversation_audit_records_20260230",
		"conversation_audit_records_202608290",
		"other_20260829",
		"conversation_audit_records_2026-08-29",
	} {
		_, ok := partitionDay(invalid)
		require.False(t, ok, invalid)
	}
}

func TestRepositoryDeleteOneWritesTombstoneBeforeDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repository := NewRepository(db)
	now := time.Now().UTC()
	createdAt := now.Add(-time.Hour)
	auditID := uuid.New()

	mock.ExpectQuery(`SELECT created_at FROM conversation_audit_records`).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(createdAt))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT record_state='finalized'`).WillReturnRows(sqlmock.NewRows([]string{"eligible"}).AddRow(true))
	mock.ExpectExec(`INSERT INTO conversation_audit_delete_tombstones`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM conversation_audit_records`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repository.DeleteOne(context.Background(), now, auditID, now))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepositoryDeleteOneRejectsRecentRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repository := NewRepository(db)
	now := time.Now().UTC()
	auditID := uuid.New()

	mock.ExpectQuery(`SELECT created_at FROM conversation_audit_records`).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now.Add(-time.Minute)))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT record_state='finalized'`).WillReturnRows(sqlmock.NewRows([]string{"eligible"}).AddRow(false))
	mock.ExpectRollback()

	require.ErrorIs(t, repository.DeleteOne(context.Background(), now, auditID, now), ErrRecordBusy)
	require.NoError(t, mock.ExpectationsWereMet())
}
