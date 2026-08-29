package conversationaudit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxListWindow      = 31 * 24 * time.Hour
	maxUnindexedWindow = 24 * time.Hour
	maxListLimit       = 100
)

var (
	ErrTimeRangeRequired = errors.New("conversation audit time range is required")
	ErrQueryLimit        = errors.New("conversation audit query limit exceeded")
)

type ListFilter struct {
	Start           time.Time
	End             time.Time
	UserID          *int64
	GroupID         *int64
	APIKeyID        *int64
	SessionID       string
	RequestID       string
	OutcomeStatus   OutcomeStatus
	CaptureStatus   CaptureStatus
	Protocol        string
	InboundEndpoint string
	RequestedModel  string
	Cursor          *RecordCursor
	Limit           int
}

type RecordMetadata struct {
	AuditID         uuid.UUID      `json:"audit_id"`
	CreatedAt       time.Time      `json:"created_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at"`
	MutableUntil    *time.Time     `json:"mutable_until,omitempty"`
	OwnerInstanceID string         `json:"-"`
	LeaseExpiresAt  *time.Time     `json:"lease_expires_at,omitempty"`
	RequestID       string         `json:"request_id"`
	SessionID       string         `json:"session_id,omitempty"`
	UserID          int64          `json:"user_id"`
	UserName        string         `json:"user_name"`
	APIKeyID        int64          `json:"api_key_id"`
	APIKeyName      string         `json:"api_key_name"`
	GroupID         *int64         `json:"group_id,omitempty"`
	GroupName       string         `json:"group_name"`
	AccountID       *int64         `json:"account_id,omitempty"`
	AccountName     string         `json:"account_name"`
	Protocol        string         `json:"protocol"`
	InboundEndpoint string         `json:"inbound_endpoint"`
	RequestedModel  string         `json:"requested_model"`
	EffectiveModel  string         `json:"effective_model"`
	TransportMode   TransportMode  `json:"transport_mode"`
	HTTPStatus      *int           `json:"http_status,omitempty"`
	ErrorCode       string         `json:"error_code"`
	RecordState     RecordState    `json:"record_state"`
	OutcomeStatus   *OutcomeStatus `json:"outcome_status,omitempty"`
	CaptureStatus   CaptureStatus  `json:"capture_status"`
	DegradedReason  string         `json:"degraded_reason"`

	RequestOriginalBytes    int64 `json:"request_original_bytes"`
	RequestStoredBytes      int64 `json:"request_stored_bytes"`
	RequestCompressedBytes  int64 `json:"request_compressed_bytes"`
	RequestEncryptedBytes   int64 `json:"request_encrypted_bytes"`
	RequestTruncated        bool  `json:"request_truncated"`
	RequestOmittedMessages  int   `json:"request_omitted_messages"`
	RequestOmittedBytes     int64 `json:"request_omitted_bytes"`
	ResponseOriginalBytes   int64 `json:"response_original_bytes"`
	ResponseStoredBytes     int64 `json:"response_stored_bytes"`
	ResponseCompressedBytes int64 `json:"response_compressed_bytes"`
	ResponseEncryptedBytes  int64 `json:"response_encrypted_bytes"`
	ResponseTruncated       bool  `json:"response_truncated"`
	ResponseOmittedMessages int   `json:"response_omitted_messages"`
	ResponseOmittedBytes    int64 `json:"response_omitted_bytes"`
}

type RecordDetail struct {
	Metadata RecordMetadata
	Request  *EncodedPayload
	Response *EncodedPayload
}

func (r *Repository) List(ctx context.Context, filter ListFilter) ([]RecordMetadata, *RecordCursor, error) {
	if r == nil || r.db == nil {
		return nil, nil, errors.New("conversation audit database is unavailable")
	}
	if err := validateListFilter(filter); err != nil {
		return nil, nil, err
	}
	where := []string{"created_at >= $1", "created_at < $2"}
	args := []any{filter.Start.UTC(), filter.End.UTC()}
	where, args = appendListFilterPredicates(where, args, filter)
	if filter.Cursor != nil {
		args = append(args, NormalizeCreatedAt(filter.Cursor.CreatedAt), filter.Cursor.AuditID)
		where = append(where, fmt.Sprintf("(created_at, audit_id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit+1)
	query := `SELECT ` + recordMetadataColumns + ` FROM conversation_audit_records WHERE ` +
		strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY created_at DESC, audit_id DESC LIMIT $%d", len(args))

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '3s'`); err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]RecordMetadata, 0, limit+1)
	for rows.Next() {
		item, err := scanRecordMetadata(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	var next *RecordCursor
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = &RecordCursor{CreatedAt: last.CreatedAt, AuditID: last.AuditID}
	}
	return items, next, nil
}

func appendListFilterPredicates(where []string, args []any, filter ListFilter) ([]string, []any) {
	add := func(predicate string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(predicate, len(args)))
	}
	if filter.UserID != nil {
		add("user_id = $%d", *filter.UserID)
	}
	if filter.GroupID != nil {
		add("group_id = $%d", *filter.GroupID)
	}
	if filter.APIKeyID != nil {
		add("api_key_id = $%d", *filter.APIKeyID)
	}
	if filter.SessionID != "" {
		add("session_id = $%d", bounded(filter.SessionID, 256))
	}
	if filter.RequestID != "" {
		add("request_id = $%d", bounded(filter.RequestID, 128))
	}
	if filter.OutcomeStatus != "" {
		add("outcome_status = $%d", string(filter.OutcomeStatus))
	}
	if filter.CaptureStatus != "" {
		add("capture_status = $%d", string(filter.CaptureStatus))
	}
	if filter.Protocol != "" {
		add("protocol = $%d", bounded(filter.Protocol, 64))
	}
	if filter.InboundEndpoint != "" {
		add("inbound_endpoint = $%d", bounded(filter.InboundEndpoint, 256))
	}
	if filter.RequestedModel != "" {
		add("requested_model = $%d", bounded(filter.RequestedModel, 256))
	}
	return where, args
}

func (r *Repository) Detail(ctx context.Context, day time.Time, auditID uuid.UUID) (RecordDetail, error) {
	if r == nil || r.db == nil || auditID == uuid.Nil {
		return RecordDetail{}, ErrRecordNotFound
	}
	start := time.Date(day.UTC().Year(), day.UTC().Month(), day.UTC().Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RecordDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '3s'`); err != nil {
		return RecordDetail{}, err
	}
	row := tx.QueryRowContext(ctx, `SELECT `+recordMetadataColumns+`,
		request_codec_version,request_key_id,request_payload,
		response_codec_version,response_key_id,response_payload
		FROM conversation_audit_records
		WHERE created_at >= $1 AND created_at < $2 AND audit_id=$3`, start, end, auditID)
	detail, err := scanRecordDetail(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RecordDetail{}, ErrRecordNotFound
	}
	if err != nil {
		return RecordDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return RecordDetail{}, err
	}
	return detail, nil
}

func validateListFilter(filter ListFilter) error {
	if filter.Start.IsZero() || filter.End.IsZero() || !filter.End.After(filter.Start) {
		return ErrTimeRangeRequired
	}
	window := filter.End.Sub(filter.Start)
	if window > maxListWindow {
		return ErrQueryLimit
	}
	if (filter.Protocol != "" || filter.InboundEndpoint != "" || filter.RequestedModel != "") && window > maxUnindexedWindow {
		return ErrQueryLimit
	}
	if filter.Limit < 0 || filter.Limit > maxListLimit {
		return ErrQueryLimit
	}
	if filter.Cursor != nil && (filter.Cursor.AuditID == uuid.Nil || filter.Cursor.CreatedAt.IsZero()) {
		return ErrInvalidCursor
	}
	return nil
}

const recordMetadataColumns = `
	audit_id,created_at,completed_at,updated_at,mutable_until,owner_instance_id,lease_expires_at,
	request_id,session_id,user_id,user_name,api_key_id,api_key_name,group_id,group_name,account_id,account_name,
	protocol,inbound_endpoint,requested_model,effective_model,transport_mode,http_status,error_code,
	record_state,outcome_status,capture_status,degraded_reason,
	request_original_bytes,request_stored_bytes,request_compressed_bytes,request_encrypted_bytes,
	request_truncated,request_omitted_messages,request_omitted_bytes,
	response_original_bytes,response_stored_bytes,response_compressed_bytes,response_encrypted_bytes,
	response_truncated,response_omitted_messages,response_omitted_bytes`

type rowScanner interface{ Scan(...any) error }

func scanRecordMetadata(row rowScanner) (RecordMetadata, error) {
	var item RecordMetadata
	nulls := metadataNulls{}
	err := row.Scan(metadataScanTargets(&item, &nulls)...)
	if err != nil {
		return RecordMetadata{}, err
	}
	applyMetadataNulls(&item, nulls)
	return item, nil
}

type metadataNulls struct {
	completedAt, mutableUntil, leaseExpiresAt sql.NullTime
	sessionID                                 sql.NullString
	groupID, accountID                        sql.NullInt64
	httpStatus                                sql.NullInt64
	outcome                                   sql.NullString
}

func metadataScanTargets(item *RecordMetadata, nulls *metadataNulls) []any {
	return []any{
		&item.AuditID, &item.CreatedAt, &nulls.completedAt, &item.UpdatedAt, &nulls.mutableUntil, &item.OwnerInstanceID, &nulls.leaseExpiresAt,
		&item.RequestID, &nulls.sessionID, &item.UserID, &item.UserName, &item.APIKeyID, &item.APIKeyName,
		&nulls.groupID, &item.GroupName, &nulls.accountID, &item.AccountName, &item.Protocol, &item.InboundEndpoint,
		&item.RequestedModel, &item.EffectiveModel, &item.TransportMode, &nulls.httpStatus, &item.ErrorCode,
		&item.RecordState, &nulls.outcome, &item.CaptureStatus, &item.DegradedReason,
		&item.RequestOriginalBytes, &item.RequestStoredBytes, &item.RequestCompressedBytes, &item.RequestEncryptedBytes,
		&item.RequestTruncated, &item.RequestOmittedMessages, &item.RequestOmittedBytes,
		&item.ResponseOriginalBytes, &item.ResponseStoredBytes, &item.ResponseCompressedBytes, &item.ResponseEncryptedBytes,
		&item.ResponseTruncated, &item.ResponseOmittedMessages, &item.ResponseOmittedBytes,
	}
}

func applyMetadataNulls(item *RecordMetadata, nulls metadataNulls) {
	item.CompletedAt = nullTimePtr(nulls.completedAt)
	item.MutableUntil = nullTimePtr(nulls.mutableUntil)
	item.LeaseExpiresAt = nullTimePtr(nulls.leaseExpiresAt)
	item.SessionID = nulls.sessionID.String
	item.GroupID = nullInt64Ptr(nulls.groupID)
	item.AccountID = nullInt64Ptr(nulls.accountID)
	if nulls.httpStatus.Valid {
		value := int(nulls.httpStatus.Int64)
		item.HTTPStatus = &value
	}
	if nulls.outcome.Valid {
		value := OutcomeStatus(nulls.outcome.String)
		item.OutcomeStatus = &value
	}
}

func scanRecordDetail(row rowScanner) (RecordDetail, error) {
	var metadata RecordMetadata
	nulls := metadataNulls{}
	var requestVersion, responseVersion sql.NullInt64
	var requestKey, responseKey sql.NullString
	var requestData, responseData []byte
	targets := metadataScanTargets(&metadata, &nulls)
	targets = append(targets, &requestVersion, &requestKey, &requestData, &responseVersion, &responseKey, &responseData)
	if err := row.Scan(targets...); err != nil {
		return RecordDetail{}, err
	}
	applyMetadataNulls(&metadata, nulls)
	detail := RecordDetail{Metadata: metadata}
	if requestVersion.Valid && requestKey.Valid && len(requestData) > 0 {
		detail.Request = &EncodedPayload{CodecVersion: int16(requestVersion.Int64), KeyID: requestKey.String, Data: requestData,
			OriginalBytes: metadata.RequestOriginalBytes, CompressedBytes: metadata.RequestCompressedBytes, EncryptedBytes: metadata.RequestEncryptedBytes}
	}
	if responseVersion.Valid && responseKey.Valid && len(responseData) > 0 {
		detail.Response = &EncodedPayload{CodecVersion: int16(responseVersion.Int64), KeyID: responseKey.String, Data: responseData,
			OriginalBytes: metadata.ResponseOriginalBytes, CompressedBytes: metadata.ResponseCompressedBytes, EncryptedBytes: metadata.ResponseEncryptedBytes}
	}
	return detail, nil
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
