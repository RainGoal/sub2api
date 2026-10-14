package requestaudit

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func banRows(id, source string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "api_key_id", "session_id", "public_reason", "internal_note", "source_audit_id", "session_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at"}).
		AddRow(id, 7, "original-session", "public reason", "internal only", source, "header", 1, time.Now(), nil, nil, nil)
}

func TestSessionBanWorksThroughPolicyPoolWhenCaptureDisabled(t *testing.T) {
	policyDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = policyDB.Close() }()
	m := NewManager(nil, WithPolicyDB(policyDB))
	m.configKnown, m.config.SessionBanEnabled = true, true
	require.False(t, m.Enabled())
	id := uuid.NewString()
	mock.ExpectQuery(`SELECT id,api_key_id`).WithArgs(int64(7), "original-session").WillReturnRows(banRows(id, uuid.NewString()))
	ban, err := m.CheckBan(context.Background(), 7, "original-session")
	require.NoError(t, err)
	require.Equal(t, id, ban.ID)
	mock.ExpectQuery(`SELECT id,api_key_id`).WithArgs(int64(8), "original-session").WillReturnError(sql.ErrNoRows)
	ban, err = m.CheckBan(context.Background(), 8, "original-session")
	require.NoError(t, err)
	require.Nil(t, ban, "same session is scoped to its original API key")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateBanUnknownCommitExposesIDForConfirmation(t *testing.T) {
	m, mock := mockManager(t)
	source := uuid.NewString()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COALESCE\(api_key_id,0\),session_id,session_source`).WithArgs(source).WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "session_id", "source"}).AddRow(7, "original-session", "header"))
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs("7:original-session").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT id FROM session_bans`).WithArgs(int64(7), "original-session").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO session_bans`).WillReturnRows(banRows(uuid.NewString(), source))
	mock.ExpectCommit().WillReturnError(errors.New("connection lost during commit"))
	_, err := m.CreateBan(context.Background(), CreateBanInput{SourceAuditID: source, PublicReason: "public reason", CreatedBy: 1})
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "BAN_RESULT_UNKNOWN", auditErr.Code)
	_, err = uuid.Parse(auditErr.Metadata["ban_id"])
	require.NoError(t, err)
}

func TestCreateBanRejectsControlCharacters(t *testing.T) {
	m, _ := mockManager(t)
	_, err := m.CreateBan(context.Background(), CreateBanInput{SourceAuditID: uuid.NewString(), PublicReason: "reason\nforged line"})
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "INVALID_BAN_REASON", auditErr.Code)
}

func TestStopCancelsInFlightWriteAndReleasesAllEvents(t *testing.T) {
	m, mock := mockManager(t)
	m.writeTimeout = 10 * time.Second
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(ConfigKey).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"retention_days":3,"version":1}`))
	mock.ExpectBegin().WillDelayFor(10 * time.Second).WillReturnError(context.DeadlineExceeded)
	require.NoError(t, m.Start(context.Background()))
	require.NotNil(t, m.Begin(RequestMeta{}))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	started := time.Now()
	require.NoError(t, m.Stop(ctx))
	require.Less(t, time.Since(started), 8*time.Second)
	require.Zero(t, m.queueBytes.Load())
	require.Zero(t, m.queueEvents.Load())
	require.False(t, m.Status().Running)
}
