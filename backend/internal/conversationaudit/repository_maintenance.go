package conversationaudit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	partitionMaintenanceLockKey int64 = 615739204788301101
	retentionMaintenanceLockKey int64 = 615739204788301102
	deleteEligibilityAge              = 5 * time.Minute
	maxFilterDeleteRecords            = 5000
	filterDeleteBatchSize             = 500
)

type CaptureRef struct {
	CreatedAt time.Time
	AuditID   uuid.UUID
}

type DeletePreviewScope struct {
	MatchedCount int
	HasMore      bool
	HighWater    *RecordCursor
	LowWater     *RecordCursor
}

type DeleteScope struct {
	Filter            ListFilter
	EligibilityCutoff time.Time
	HighWater         RecordCursor
	LowWater          RecordCursor
}

func (r *Repository) RenewLeases(ctx context.Context, owner string, captures []CaptureRef, expiresAt time.Time) error {
	if r == nil || r.db == nil || owner == "" || len(captures) == 0 {
		return nil
	}
	createdValues := make([]time.Time, 0, len(captures))
	auditValues := make([]string, 0, len(captures))
	for _, capture := range captures {
		if capture.AuditID == uuid.Nil || capture.CreatedAt.IsZero() {
			continue
		}
		createdValues = append(createdValues, NormalizeCreatedAt(capture.CreatedAt))
		auditValues = append(auditValues, capture.AuditID.String())
	}
	if len(createdValues) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE conversation_audit_records AS records
		SET lease_expires_at=$1, updated_at=NOW()
		FROM unnest($2::timestamptz[], $3::uuid[]) AS active(created_at, audit_id)
		WHERE records.created_at=active.created_at AND records.audit_id=active.audit_id
			AND records.owner_instance_id=$4 AND records.record_state='capturing'`,
		expiresAt.UTC(), pq.Array(createdValues), pq.Array(auditValues), owner)
	return err
}

func (r *Repository) EnsurePartitions(ctx context.Context, now time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("conversation audit database is unavailable")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, partitionMaintenanceLockKey); err != nil {
		return err
	}
	today := utcDay(now)
	for offset := 0; offset <= 2; offset++ {
		if _, err := tx.ExecContext(ctx, `SELECT conversation_audit_create_daily_partition($1)`, today.AddDate(0, 0, offset)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) FinalizeExpiredCaptures(ctx context.Context, limit int) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("conversation audit database is unavailable")
	}
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	result, err := r.db.ExecContext(ctx, `
		WITH expired AS (
			SELECT created_at,audit_id FROM conversation_audit_records
			WHERE record_state='capturing'
				AND (lease_expires_at IS NULL OR lease_expires_at < transaction_timestamp())
			ORDER BY created_at,audit_id FOR UPDATE SKIP LOCKED LIMIT $1
		)
		UPDATE conversation_audit_records AS records SET
			record_state='finalized',outcome_status='unknown',capture_status='degraded',
			degraded_reason='owner_lease_expired',completed_at=transaction_timestamp(),
			mutable_until=transaction_timestamp()+INTERVAL '2 minutes',lease_expires_at=NULL,updated_at=NOW()
		FROM expired
		WHERE records.created_at=expired.created_at AND records.audit_id=expired.audit_id`, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *Repository) DeleteOne(ctx context.Context, day time.Time, auditID uuid.UUID, now time.Time) error {
	if r == nil || r.db == nil || auditID == uuid.Nil {
		return ErrRecordNotFound
	}
	start := utcDay(day)
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT created_at FROM conversation_audit_records
		WHERE created_at >= $1 AND created_at < $2 AND audit_id=$3
		ORDER BY created_at LIMIT 1`, start, start.AddDate(0, 0, 1), auditID).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRecordNotFound
	}
	if err != nil {
		return err
	}
	createdAt = NormalizeCreatedAt(createdAt)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, recordAdvisoryLockKey(createdAt, auditID)); err != nil {
		return err
	}
	var eligible bool
	if err := tx.QueryRowContext(ctx, `
		SELECT record_state='finalized' AND completed_at <= $3
		FROM conversation_audit_records WHERE created_at=$1 AND audit_id=$2
		FOR UPDATE`, createdAt, auditID, now.UTC().Add(-deleteEligibilityAge)).Scan(&eligible); errors.Is(err, sql.ErrNoRows) {
		return ErrRecordNotFound
	} else if err != nil {
		return err
	}
	if !eligible {
		return ErrRecordBusy
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_audit_delete_tombstones (created_at,audit_id,deleted_at)
		VALUES ($1,$2,$3) ON CONFLICT (created_at,audit_id) DO NOTHING`, createdAt, auditID, now.UTC()); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM conversation_audit_records WHERE created_at=$1 AND audit_id=$2`, createdAt, auditID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrRecordNotFound
	}
	return tx.Commit()
}

func (r *Repository) PreviewDelete(ctx context.Context, filter ListFilter, cutoff time.Time) (DeletePreviewScope, error) {
	if r == nil || r.db == nil {
		return DeletePreviewScope{}, errors.New("conversation audit database is unavailable")
	}
	filter.Cursor = nil
	filter.Limit = 0
	if cutoff.IsZero() {
		return DeletePreviewScope{}, errors.New("conversation audit delete cutoff is required")
	}
	if err := validateListFilter(filter); err != nil {
		return DeletePreviewScope{}, err
	}
	where := []string{"created_at >= $1", "created_at < $2", "record_state='finalized'", "completed_at <= $3"}
	args := []any{filter.Start.UTC(), filter.End.UTC(), cutoff.UTC()}
	where, args = appendListFilterPredicates(where, args, filter)
	args = append(args, maxFilterDeleteRecords+1)
	query := `SELECT created_at,audit_id FROM conversation_audit_records WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY created_at DESC,audit_id DESC LIMIT $%d", len(args))

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DeletePreviewScope{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '3s'`); err != nil {
		return DeletePreviewScope{}, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return DeletePreviewScope{}, err
	}
	refs, err := scanCaptureRefs(rows, maxFilterDeleteRecords+1)
	_ = rows.Close()
	if err != nil {
		return DeletePreviewScope{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeletePreviewScope{}, err
	}
	result := DeletePreviewScope{MatchedCount: len(refs)}
	if len(refs) > maxFilterDeleteRecords {
		result.MatchedCount = maxFilterDeleteRecords
		result.HasMore = true
		refs = refs[:maxFilterDeleteRecords]
	}
	if len(refs) > 0 {
		result.HighWater = &RecordCursor{CreatedAt: refs[0].CreatedAt, AuditID: refs[0].AuditID}
		last := refs[len(refs)-1]
		result.LowWater = &RecordCursor{CreatedAt: last.CreatedAt, AuditID: last.AuditID}
	}
	return result, nil
}

func (r *Repository) DeleteByScope(ctx context.Context, scope DeleteScope, deletedAt time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("conversation audit database is unavailable")
	}
	scope.Filter.Cursor = nil
	scope.Filter.Limit = 0
	if deletedAt.IsZero() || scope.EligibilityCutoff.IsZero() ||
		scope.HighWater.AuditID == uuid.Nil || scope.LowWater.AuditID == uuid.Nil ||
		scope.HighWater.CreatedAt.IsZero() || scope.LowWater.CreatedAt.IsZero() ||
		!cursorAtOrAfter(scope.HighWater, scope.LowWater) {
		return 0, errors.New("conversation audit delete scope is invalid")
	}
	if err := validateListFilter(scope.Filter); err != nil {
		return 0, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '8s'`); err != nil {
		return 0, err
	}
	refs, err := selectDeleteScopeRefs(ctx, tx, scope)
	if err != nil {
		return 0, err
	}
	if len(refs) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if len(refs) > maxFilterDeleteRecords {
		return 0, errors.New("conversation audit delete scope exceeds limit")
	}
	lockKeys := make([]int64, len(refs))
	for i, ref := range refs {
		lockKeys[i] = recordAdvisoryLockKey(ref.CreatedAt, ref.AuditID)
	}
	lockRows, err := tx.QueryContext(ctx, `
		SELECT ordinal,pg_advisory_xact_lock(lock_key)
		FROM unnest($1::bigint[]) WITH ORDINALITY AS locks(lock_key, ordinal)
		ORDER BY ordinal`, pq.Array(lockKeys))
	if err != nil {
		return 0, err
	}
	acquired := 0
	for lockRows.Next() {
		var ordinal int64
		var lockResult any
		if err := lockRows.Scan(&ordinal, &lockResult); err != nil {
			_ = lockRows.Close()
			return 0, err
		}
		if ordinal != int64(acquired+1) {
			_ = lockRows.Close()
			return 0, errors.New("conversation audit delete lock order is invalid")
		}
		acquired++
	}
	if err := lockRows.Close(); err != nil {
		return 0, err
	}
	if err := lockRows.Err(); err != nil {
		return 0, err
	}
	if acquired != len(refs) {
		return 0, errors.New("conversation audit delete lock acquisition failed")
	}

	var deleted int64
	for start := 0; start < len(refs); start += filterDeleteBatchSize {
		end := start + filterDeleteBatchSize
		if end > len(refs) {
			end = len(refs)
		}
		count, err := deleteRefBatch(ctx, tx, refs[start:end], scope.EligibilityCutoff, deletedAt)
		if err != nil {
			return 0, err
		}
		deleted += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func selectDeleteScopeRefs(ctx context.Context, tx *sql.Tx, scope DeleteScope) ([]CaptureRef, error) {
	where := []string{
		"created_at >= $1", "created_at < $2", "record_state='finalized'", "completed_at <= $3",
		"(created_at,audit_id) <= ($4,$5)", "(created_at,audit_id) >= ($6,$7)",
	}
	args := []any{
		scope.Filter.Start.UTC(), scope.Filter.End.UTC(), scope.EligibilityCutoff.UTC(),
		NormalizeCreatedAt(scope.HighWater.CreatedAt), scope.HighWater.AuditID,
		NormalizeCreatedAt(scope.LowWater.CreatedAt), scope.LowWater.AuditID,
	}
	where, args = appendListFilterPredicates(where, args, scope.Filter)
	args = append(args, maxFilterDeleteRecords+1)
	query := `SELECT created_at,audit_id FROM conversation_audit_records WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY created_at ASC,audit_id ASC LIMIT $%d", len(args))
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCaptureRefs(rows, maxFilterDeleteRecords+1)
}

func scanCaptureRefs(rows *sql.Rows, capacity int) ([]CaptureRef, error) {
	refs := make([]CaptureRef, 0, capacity)
	for rows.Next() {
		var ref CaptureRef
		if err := rows.Scan(&ref.CreatedAt, &ref.AuditID); err != nil {
			return nil, err
		}
		ref.CreatedAt = NormalizeCreatedAt(ref.CreatedAt)
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func deleteRefBatch(ctx context.Context, tx *sql.Tx, refs []CaptureRef, cutoff, deletedAt time.Time) (int64, error) {
	createdValues := make([]time.Time, len(refs))
	auditValues := make([]string, len(refs))
	for i, ref := range refs {
		createdValues[i] = NormalizeCreatedAt(ref.CreatedAt)
		auditValues[i] = ref.AuditID.String()
	}
	if _, err := tx.ExecContext(ctx, `WITH candidates AS (
		SELECT * FROM unnest($1::timestamptz[],$2::uuid[]) AS selected(created_at,audit_id)
	)
	INSERT INTO conversation_audit_delete_tombstones(created_at,audit_id,deleted_at)
	SELECT records.created_at,records.audit_id,$4
	FROM conversation_audit_records records
	JOIN candidates USING(created_at,audit_id)
	WHERE records.record_state='finalized' AND records.completed_at <= $3
	ON CONFLICT(created_at,audit_id) DO NOTHING`,
		pq.Array(createdValues), pq.Array(auditValues), cutoff.UTC(), deletedAt.UTC()); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `WITH candidates AS (
		SELECT * FROM unnest($1::timestamptz[],$2::uuid[]) AS selected(created_at,audit_id)
	)
	DELETE FROM conversation_audit_records records USING candidates
	WHERE records.created_at=candidates.created_at AND records.audit_id=candidates.audit_id
		AND records.record_state='finalized' AND records.completed_at <= $3`,
		pq.Array(createdValues), pq.Array(auditValues), cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *Repository) DropExpiredPartitions(ctx context.Context, now time.Time, retentionDays int) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("conversation audit database is unavailable")
	}
	if retentionDays < 1 || retentionDays > 365 {
		return nil, errors.New("conversation audit retention is invalid")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT child.relname
		FROM pg_inherits
		JOIN pg_class parent ON parent.oid=inhparent
		JOIN pg_class child ON child.oid=inhrelid
		WHERE parent.oid='conversation_audit_records'::regclass
		ORDER BY child.relname`)
	if err != nil {
		return nil, err
	}
	var candidates []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		day, ok := partitionDay(name)
		if ok && day.Before(utcDay(now).AddDate(0, 0, -(retentionDays-1))) {
			candidates = append(candidates, name)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	dropped := make([]string, 0, len(candidates))
	for _, name := range candidates {
		ok, err := r.dropExpiredPartition(ctx, name)
		if err != nil {
			return dropped, err
		}
		if ok {
			dropped = append(dropped, name)
		}
	}
	return dropped, nil
}

func (r *Repository) dropExpiredPartition(ctx context.Context, name string) (bool, error) {
	day, ok := partitionDay(name)
	if !ok {
		return false, errors.New("conversation audit partition name is invalid")
	}
	quoted := pq.QuoteIdentifier(name)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, retentionMaintenanceLockKey); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE `+quoted+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+quoted+` SET
		record_state='finalized',outcome_status='unknown',capture_status='degraded',
		degraded_reason='owner_lease_expired',completed_at=transaction_timestamp(),
		mutable_until=transaction_timestamp()+INTERVAL '2 minutes',lease_expires_at=NULL,updated_at=NOW()
		WHERE record_state='capturing' AND (lease_expires_at IS NULL OR lease_expires_at < transaction_timestamp())`); err != nil {
		return false, err
	}
	var live bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM `+quoted+` WHERE record_state='capturing'
			AND lease_expires_at >= transaction_timestamp()
	)`).Scan(&live); err != nil {
		return false, err
	}
	if live {
		return false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE `+quoted); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM conversation_audit_delete_tombstones
		WHERE created_at >= $1 AND created_at < $2`, day, day.AddDate(0, 0, 1)); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func partitionDay(name string) (time.Time, bool) {
	const prefix = "conversation_audit_records_"
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+8 {
		return time.Time{}, false
	}
	value, err := time.ParseInLocation("20060102", strings.TrimPrefix(name, prefix), time.UTC)
	return value, err == nil
}
