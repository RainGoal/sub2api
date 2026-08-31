package conversationaudit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrRecordDeleted  = errors.New("conversation audit record was deleted")
	ErrRecordStale    = errors.New("conversation audit record is no longer mutable")
	ErrRecordNotFound = errors.New("conversation audit record not found")
	ErrRecordBusy     = errors.New("conversation audit record is not eligible for deletion")
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Upsert(ctx context.Context, write RecordWrite) error {
	if r == nil || r.db == nil {
		return errors.New("conversation audit database is unavailable")
	}
	if write.TransportMode == "" {
		write.TransportMode = TransportHTTP
	}
	if write.CaptureStatus == "" {
		write.CaptureStatus = CaptureMetadataOnly
	}
	if err := validateRecordWrite(write); err != nil {
		return err
	}
	write.CreatedAt = NormalizeCreatedAt(write.CreatedAt)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, recordAdvisoryLockKey(write.CreatedAt, write.AuditID)); err != nil {
		return err
	}
	var deleted bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM conversation_audit_delete_tombstones
			WHERE created_at=$1 AND audit_id=$2
		)`, write.CreatedAt, write.AuditID).Scan(&deleted); err != nil {
		return err
	}
	if deleted {
		return ErrRecordDeleted
	}

	result, err := tx.ExecContext(ctx, upsertRecordSQL,
		write.AuditID, write.CreatedAt, write.CompletedAt, write.MutableUntil,
		write.OwnerInstanceID, write.LeaseExpiresAt,
		write.RequestID, nullString(write.SessionID), write.UserID, bounded(write.UserName, 128),
		write.APIKeyID, bounded(write.APIKeyName, 128), write.GroupID, bounded(write.GroupName, 128),
		write.AccountID, bounded(write.AccountName, 128), bounded(write.Protocol, 64),
		bounded(write.InboundEndpoint, 256), bounded(write.RequestedModel, 256), bounded(write.EffectiveModel, 256),
		string(write.TransportMode), write.HTTPStatus, bounded(write.ErrorCode, 128), string(write.RecordState),
		nullOutcome(write.OutcomeStatus), string(write.CaptureStatus), bounded(write.DegradedReason, 128),
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrRecordStale
	}
	if write.Payload != nil {
		if err := updatePayload(ctx, tx, write.CreatedAt, write.AuditID, write.OwnerInstanceID, write.Payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const upsertRecordSQL = `
INSERT INTO conversation_audit_records (
	audit_id,created_at,completed_at,mutable_until,owner_instance_id,lease_expires_at,
	request_id,session_id,user_id,user_name,api_key_id,api_key_name,group_id,group_name,
	account_id,account_name,protocol,inbound_endpoint,requested_model,effective_model,
	transport_mode,http_status,error_code,record_state,outcome_status,capture_status,degraded_reason
) VALUES (
	$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27
)
ON CONFLICT (created_at, audit_id) DO UPDATE SET
	completed_at=CASE WHEN EXCLUDED.record_state='finalized' THEN EXCLUDED.completed_at ELSE conversation_audit_records.completed_at END,
	mutable_until=CASE WHEN EXCLUDED.record_state='finalized' THEN EXCLUDED.mutable_until ELSE conversation_audit_records.mutable_until END,
	lease_expires_at=CASE WHEN EXCLUDED.record_state='finalized' THEN NULL ELSE EXCLUDED.lease_expires_at END,
	session_id=COALESCE(EXCLUDED.session_id, conversation_audit_records.session_id),
	group_id=COALESCE(EXCLUDED.group_id, conversation_audit_records.group_id),
	group_name=CASE WHEN EXCLUDED.group_name<>'' THEN EXCLUDED.group_name ELSE conversation_audit_records.group_name END,
	account_id=COALESCE(EXCLUDED.account_id, conversation_audit_records.account_id),
	account_name=CASE WHEN EXCLUDED.account_name<>'' THEN EXCLUDED.account_name ELSE conversation_audit_records.account_name END,
	requested_model=CASE WHEN EXCLUDED.requested_model<>'' THEN EXCLUDED.requested_model ELSE conversation_audit_records.requested_model END,
	effective_model=CASE WHEN EXCLUDED.effective_model<>'' THEN EXCLUDED.effective_model ELSE conversation_audit_records.effective_model END,
	http_status=COALESCE(EXCLUDED.http_status, conversation_audit_records.http_status),
	error_code=CASE WHEN EXCLUDED.error_code<>'' THEN EXCLUDED.error_code ELSE conversation_audit_records.error_code END,
	record_state=CASE WHEN conversation_audit_records.record_state='finalized' THEN 'finalized' ELSE EXCLUDED.record_state END,
	outcome_status=COALESCE(EXCLUDED.outcome_status, conversation_audit_records.outcome_status),
	capture_status=CASE
		WHEN conversation_audit_records.capture_status='degraded' OR EXCLUDED.capture_status='degraded' THEN 'degraded'
		WHEN conversation_audit_records.capture_status='truncated' OR EXCLUDED.capture_status='truncated' THEN 'truncated'
		WHEN conversation_audit_records.capture_status='complete' OR EXCLUDED.capture_status='complete' THEN 'complete'
		ELSE 'metadata_only' END,
	degraded_reason=CASE WHEN EXCLUDED.degraded_reason<>'' THEN EXCLUDED.degraded_reason ELSE conversation_audit_records.degraded_reason END,
	updated_at=NOW()
WHERE conversation_audit_records.owner_instance_id=EXCLUDED.owner_instance_id
	AND (
		conversation_audit_records.record_state='capturing'
		OR conversation_audit_records.mutable_until >= transaction_timestamp()
	)`

func updatePayload(ctx context.Context, tx *sql.Tx, createdAt time.Time, auditID uuid.UUID, owner string, payload *StoredPayload) error {
	var query string
	switch payload.Side {
	case PayloadSideRequest:
		query = `UPDATE conversation_audit_records SET
			request_original_bytes=$4,request_stored_bytes=$5,request_compressed_bytes=$6,request_encrypted_bytes=$7,
			request_truncated=$8,request_omitted_messages=$9,request_omitted_bytes=$10,
			request_codec_version=$11,request_key_id=$12,request_payload=$13,updated_at=NOW()
			WHERE created_at=$1 AND audit_id=$2 AND owner_instance_id=$3
			AND (record_state='capturing' OR mutable_until >= transaction_timestamp())`
	case PayloadSideResponse:
		query = `UPDATE conversation_audit_records SET
			response_original_bytes=$4,response_stored_bytes=$5,response_compressed_bytes=$6,response_encrypted_bytes=$7,
			response_truncated=$8,response_omitted_messages=$9,response_omitted_bytes=$10,
			response_codec_version=$11,response_key_id=$12,response_payload=$13,updated_at=NOW()
			WHERE created_at=$1 AND audit_id=$2 AND owner_instance_id=$3
			AND (record_state='capturing' OR mutable_until >= transaction_timestamp())`
	default:
		return errors.New("conversation audit payload side is invalid")
	}
	result, err := tx.ExecContext(ctx, query,
		createdAt, auditID, owner, payload.OriginalBytes, payload.StoredBytes,
		payload.CompressedBytes, payload.EncryptedBytes, payload.Truncated,
		payload.OmittedMessages, payload.OmittedBytes, payload.CodecVersion,
		payload.KeyID, payload.Data,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrRecordStale
	}
	return nil
}

func validateRecordWrite(write RecordWrite) error {
	if write.AuditID == uuid.Nil || write.CreatedAt.IsZero() || write.OwnerInstanceID == "" ||
		write.RequestID == "" || write.UserID <= 0 || write.APIKeyID <= 0 ||
		write.Protocol == "" || write.InboundEndpoint == "" {
		return errors.New("conversation audit record identity is invalid")
	}
	if write.TransportMode != TransportHTTP && write.TransportMode != TransportSSE && write.TransportMode != TransportWebSocket {
		return errors.New("conversation audit transport mode is invalid")
	}
	if write.RecordState != RecordStateCapturing && write.RecordState != RecordStateFinalized {
		return errors.New("conversation audit record state is invalid")
	}
	if write.RecordState == RecordStateFinalized {
		if write.CompletedAt == nil || write.MutableUntil == nil || write.OutcomeStatus == nil {
			return errors.New("conversation audit finalized record is incomplete")
		}
	} else if write.CompletedAt != nil || write.OutcomeStatus != nil {
		return errors.New("conversation audit capturing record has terminal fields")
	}
	switch write.CaptureStatus {
	case CaptureComplete, CaptureTruncated, CaptureMetadataOnly, CaptureDegraded:
	default:
		return errors.New("conversation audit capture status is invalid")
	}
	if write.OutcomeStatus != nil {
		switch *write.OutcomeStatus {
		case OutcomeCompleted, OutcomeError, OutcomeTimeout, OutcomePartial, OutcomeCancelled, OutcomeUnknown:
		default:
			return errors.New("conversation audit outcome status is invalid")
		}
	}
	return nil
}

func recordAdvisoryLockKey(createdAt time.Time, auditID uuid.UUID) int64 {
	digest := sha256.New()
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(NormalizeCreatedAt(createdAt).UnixMicro()))
	_, _ = digest.Write(timestamp[:])
	_, _ = digest.Write(auditID[:])
	value := digest.Sum(nil)
	return int64(binary.BigEndian.Uint64(value[:8]))
}

func nullOutcome(value *OutcomeStatus) any {
	if value == nil {
		return nil
	}
	return string(*value)
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return bounded(value, 256)
}

func bounded(value string, limit int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if len(value) <= limit {
		return value
	}
	return truncateUTF8(value, limit)
}
