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

func TestRepositoryPreviewDeleteFixesBoundedWatermarks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repository := NewRepository(db)
	now := time.Now().UTC()
	newer := CaptureRef{CreatedAt: now.Add(-time.Hour), AuditID: uuid.New()}
	older := CaptureRef{CreatedAt: now.Add(-2 * time.Hour), AuditID: uuid.New()}

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL statement_timeout`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT created_at,audit_id FROM conversation_audit_records`).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "audit_id"}).
			AddRow(newer.CreatedAt, newer.AuditID).
			AddRow(older.CreatedAt, older.AuditID))
	mock.ExpectCommit()

	preview, err := repository.PreviewDelete(context.Background(), ListFilter{
		Start: now.Add(-24 * time.Hour), End: now,
	}, now.Add(-deleteEligibilityAge))
	require.NoError(t, err)
	require.Equal(t, 2, preview.MatchedCount)
	require.False(t, preview.HasMore)
	require.Equal(t, newer.AuditID, preview.HighWater.AuditID)
	require.Equal(t, older.AuditID, preview.LowWater.AuditID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepositoryDeleteByScopeLocksTombstonesAndDeletesAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repository := NewRepository(db)
	now := time.Now().UTC()
	older := CaptureRef{CreatedAt: now.Add(-2 * time.Hour), AuditID: uuid.New()}
	newer := CaptureRef{CreatedAt: now.Add(-time.Hour), AuditID: uuid.New()}
	scope := DeleteScope{
		Filter:            ListFilter{Start: now.Add(-24 * time.Hour), End: now},
		EligibilityCutoff: now.Add(-deleteEligibilityAge),
		HighWater:         RecordCursor{CreatedAt: newer.CreatedAt, AuditID: newer.AuditID},
		LowWater:          RecordCursor{CreatedAt: older.CreatedAt, AuditID: older.AuditID},
	}

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL statement_timeout`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT created_at,audit_id FROM conversation_audit_records`).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "audit_id"}).
			AddRow(older.CreatedAt, older.AuditID).
			AddRow(newer.CreatedAt, newer.AuditID))
	mock.ExpectQuery(`SELECT ordinal,pg_advisory_xact_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"ordinal", "lock"}).AddRow(1, nil).AddRow(2, nil))
	mock.ExpectExec(`INSERT INTO conversation_audit_delete_tombstones`).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`DELETE FROM conversation_audit_records`).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	deleted, err := repository.DeleteByScope(context.Background(), scope, now)
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
