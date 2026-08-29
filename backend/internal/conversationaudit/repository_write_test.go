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

func validRecordWrite() RecordWrite {
	lease := time.Now().UTC().Add(2 * time.Minute)
	return RecordWrite{
		AuditID: uuid.New(), CreatedAt: time.Now().UTC(), OwnerInstanceID: "instance-1", LeaseExpiresAt: &lease,
		RequestID: "request-1", UserID: 1, APIKeyID: 2, Protocol: "openai_responses",
		InboundEndpoint: "/v1/responses", RecordState: RecordStateCapturing, CaptureStatus: CaptureMetadataOnly,
	}
}

func TestRepositoryUpsertSerializesTombstoneCheckAndPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repository := NewRepository(db)
	write := validRecordWrite()
	write.Payload = &StoredPayload{
		Side: PayloadSideRequest, CodecVersion: PayloadCodecVersion, KeyID: "v1", Data: []byte{1, 2, 3},
		OriginalBytes: 100, StoredBytes: 90, CompressedBytes: 50, EncryptedBytes: 78,
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(`INSERT INTO conversation_audit_records`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE conversation_audit_records SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repository.Upsert(context.Background(), write))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepositoryUpsertStopsAtCommittedTombstone(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repository := NewRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	require.ErrorIs(t, repository.Upsert(context.Background(), validRecordWrite()), ErrRecordDeleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepositoryRejectsIncompleteFinalRecordBeforeDatabase(t *testing.T) {
	repository := &Repository{}
	write := validRecordWrite()
	write.RecordState = RecordStateFinalized
	require.Error(t, repository.Upsert(context.Background(), write))
}
