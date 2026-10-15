package requestaudit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func mockManager(t *testing.T) (*Manager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	return NewManager(db), mock
}

func recordRows(id, state, reason string, manifests map[string]PartManifest, metadata ...driver.Value) *sqlmock.Rows {
	columns := strings.Split(strings.TrimSuffix(strings.TrimPrefix(recordSelect, "SELECT "), " FROM request_audits"), ",")
	columns = append(columns, "user_username", "user_email", "api_key_name", "group_platform", "group_name")
	var display [5]driver.Value
	copy(display[:], metadata)
	now := time.Now()
	return sqlmock.NewRows(columns).AddRow(id, now, now, "http_request", "test", "POST", "/v1/messages", "HTTP/1.1", nil, nil, nil, nil,
		1, 2, 3, "session", "header", "resolved", "test-model", 200, 1, "completed", nil, state, reason, jsonBytes(manifests), 0, 0, now, now,
		"codex", "Original-Device", "header", "resolved",
		display[0], display[1], display[2], display[3], display[4])
}

func expectRecord(mock sqlmock.Sqlmock, id, state, reason string, manifests map[string]PartManifest) {
	mock.ExpectQuery(regexp.QuoteMeta(withRecordMetadata(recordSelect + ` WHERE id=$1 AND ` + visibleRecordSQL))).WithArgs(id).WillReturnRows(recordRows(id, state, reason, manifests))
}

func expectRetention(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock_shared(hashtext($1))`)).WithArgs(ConfigKey).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + retentionDaysSQL)).WillReturnRows(sqlmock.NewRows([]string{"days"}).AddRow(3))
}

func TestReadPayloadRejectsMissingTailDespiteCompleteMetadata(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	expectRecord(mock, id, "complete", "", map[string]PartManifest{PartBody: {ExpectedChunks: 2, ExpectedBytes: 6, Complete: true}})
	mock.ExpectQuery(`SELECT c.seq,c.byte_count`).WithArgs(id, PartBody).WillReturnRows(sqlmock.NewRows([]string{"seq", "byte_count"}).AddRow(0, 3))
	_, err := m.ReadPayload(context.Background(), id, PartBody, 0, 6, false)
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "AUDIT_PAYLOAD_INCOMPLETE", auditErr.Code)
}

func TestReadPayloadPartialReturnsOnlyPrefixBeforeMissingChunk(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	expectRecord(mock, id, "partial", "missing_chunks", map[string]PartManifest{PartBody: {ExpectedChunks: 3, ExpectedBytes: 9}})
	mock.ExpectQuery(`SELECT c.seq,c.byte_count`).WithArgs(id, PartBody).WillReturnRows(sqlmock.NewRows([]string{"seq", "byte_count"}).AddRow(0, 3).AddRow(2, 3))
	mock.ExpectQuery(`WITH positions AS`).WithArgs(id, PartBody, 1, int64(1), int64(3)).WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow([]byte("bc")))
	result, err := m.ReadPayload(context.Background(), id, PartBody, 1, 100, true)
	require.NoError(t, err)
	require.Equal(t, []byte("bc"), result.Bytes)
	require.EqualValues(t, 3, result.TotalBytes)
	require.True(t, result.Partial)
}

func TestReadPayloadDetectsRemovalDuringRead(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	expectRecord(mock, id, "complete", "", map[string]PartManifest{PartBody: {ExpectedChunks: 1, ExpectedBytes: 3, Complete: true}})
	mock.ExpectQuery(`SELECT c.seq,c.byte_count`).WillReturnRows(sqlmock.NewRows([]string{"seq", "byte_count"}).AddRow(0, 3))
	mock.ExpectQuery(`WITH positions AS`).WillReturnRows(sqlmock.NewRows([]string{"payload"}))
	_, err := m.ReadPayload(context.Background(), id, PartBody, 0, 3, false)
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "AUDIT_PAYLOAD_INCOMPLETE", auditErr.Code)
}

func TestReadPayloadEnforcesBoundAndEndMarker(t *testing.T) {
	m, mock := mockManager(t)
	_, err := m.ReadPayload(context.Background(), uuid.NewString(), PartBody, 0, MaxPayloadRead+1, false)
	require.Error(t, err)
	id := uuid.NewString()
	expectRecord(mock, id, "capturing", "", nil)
	_, err = m.ReadPayload(context.Background(), id, PartBody, 0, 1, false)
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "AUDIT_PAYLOAD_NOT_READY", auditErr.Code)
}

func TestPersistDroppedParentDoesNotPoisonOtherRequests(t *testing.T) {
	m, mock := mockManager(t)
	id, otherID := uuid.NewString(), uuid.NewString()
	mock.ExpectBegin()
	expectRetention(mock)
	mock.ExpectExec(`INSERT INTO request_audit_payload_chunks`).WithArgs(id, PartBody, 0, []byte("lost"), 4, 3).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT payload=\$4`).WithArgs(id, PartBody, 0, []byte("lost")).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO request_audits`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err := m.persistBatch(context.Background(), []auditEvent{{kind: "chunk", id: id, part: PartBody, raw: []byte("lost")}, {kind: "begin", id: otherID, meta: &RequestMeta{ID: otherID, ReceivedAt: time.Now()}}})
	require.NoError(t, err)
}

func TestReconcilePreservesConflictAcrossLaterFinish(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	manifests := map[string]PartManifest{PartHeaders: {Complete: true}, PartBody: {Complete: false, Reason: "read_error"}, PartTrailers: {Complete: true}}
	mock.ExpectBegin()
	tx, err := m.db.Begin()
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT terminal_manifest,capture_state,capture_reason`).WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"terminal_manifest", "capture_state", "capture_reason"}).AddRow(jsonBytes(manifests), "partial", "chunk_conflict"))
	mock.ExpectQuery(`SELECT part,COUNT`).WillReturnRows(sqlmock.NewRows([]string{"part", "count", "bytes", "min", "max"}))
	mock.ExpectExec(`UPDATE request_audits SET capture_state`).WithArgs(id, "partial", "chunk_conflict", string(jsonBytes(manifests)), int64(0), int64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, reconcileRecord(context.Background(), tx, id))
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
}

func TestFlushFailureReleasesBudgetAndBacksOff(t *testing.T) {
	m, mock := mockManager(t)
	mock.ExpectBegin().WillReturnError(errors.New("storage offline"))
	batch := []auditEvent{{kind: "begin", id: uuid.NewString(), createdAt: time.Now(), queued: 512}}
	m.queueBytes.Store(512)
	m.queueEvents.Store(1)
	m.flush(context.Background(), batch)
	require.Zero(t, m.queueBytes.Load())
	require.Zero(t, m.queueEvents.Load())
	require.EqualValues(t, 1, m.writeFailures.Load())
	require.Greater(t, m.circuitUntil.Load(), time.Now().UnixNano())
}

func TestConfigValidationAndCursor(t *testing.T) {
	for _, days := range []int{0, -1, 31} {
		require.Error(t, validateConfig(Config{RetentionDays: days}))
	}
	for _, days := range []int{1, 3, 30} {
		require.NoError(t, validateConfig(Config{RetentionDays: days}))
	}
	now, id := time.Now().UTC(), uuid.NewString()
	gotTime, gotID, err := decodeCursor(encodeCursor(now, id))
	require.NoError(t, err)
	require.Equal(t, now, gotTime)
	require.Equal(t, id, gotID)
	_, _, err = decodeCursor(encodeCursor(now, "not-a-uuid"))
	require.Error(t, err)
	m, mock := mockManager(t)
	raw, err := json.Marshal(Config{RetentionDays: 3})
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(ConfigKey).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
	cfg, err := m.loadConfig(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, cfg.Version)
}
