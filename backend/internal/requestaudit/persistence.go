package requestaudit

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Every read evaluates the committed retention setting in the same statement.
// Invalid settings fail closed instead of exposing data with a cached window.
const retentionMinutesSQL = `COALESCE((SELECT COALESCE((value::jsonb->>'retention_minutes')::int,(value::jsonb->>'retention_days')::int*1440) FROM settings WHERE key='request_audit_config'),4320)`
const visibleRecordSQL = `cleanup_state='active' AND ` + retentionMinutesSQL + ` BETWEEN 1 AND 43200 AND received_at >= NOW() - (` + retentionMinutesSQL + ` * INTERVAL '1 minute')`

func validPart(part string) bool {
	return part == PartHeaders || part == PartBody || part == PartTrailers
}

// Serialize retention changes against a writer/cleanup transaction, including
// the initial configuration insert when no settings row exists yet.
func lockRetention(ctx context.Context, tx *sql.Tx) (int, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext($1))`, ConfigKey); err != nil {
		return 0, err
	}
	var minutes int
	if err := tx.QueryRowContext(ctx, `SELECT `+retentionMinutesSQL).Scan(&minutes); err != nil {
		return 0, err
	}
	if err := validateRetentionMinutes(minutes); err != nil {
		return 0, err
	}
	return minutes, nil
}

func persistChunk(ctx context.Context, tx *sql.Tx, e auditEvent, retention int) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO request_audit_payload_chunks(audit_id,part,seq,payload,byte_count,codec)
SELECT id,$2,$3,$4,$5,$7 FROM (SELECT id FROM request_audits WHERE id=$1 AND cleanup_state='active'
AND received_at >= NOW() - ($6::int * INTERVAL '1 minute') FOR SHARE) parent
ON CONFLICT(audit_id,part,seq) DO NOTHING`, e.id, e.part, e.seq, eventPayload(e), len(e.raw), retention, eventCodec(e))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE request_audits SET updated_at=NOW(),last_event_at=NOW(),stored_body_bytes=stored_body_bytes+CASE WHEN $2='body' THEN $3 ELSE 0 END WHERE id=$1`, e.id, e.part, len(e.raw))
		return err
	}
	var data []byte
	var codec, size int
	err = tx.QueryRowContext(ctx, `SELECT c.payload,c.codec,c.byte_count FROM request_audit_payload_chunks c
JOIN request_audits r ON r.id=c.audit_id WHERE c.audit_id=$1 AND c.part=$2 AND c.seq=$3
AND r.cleanup_state='active' AND r.received_at >= NOW() - ($4::int * INTERVAL '1 minute') FOR SHARE OF r`,
		e.id, e.part, e.seq, retention).Scan(&data, &codec, &size)
	if errors.Is(err, sql.ErrNoRows) {
		// A dropped begin or expired parent must not poison unrelated records.
		return nil
	}
	if err != nil {
		return err
	}
	data, err = decodeBlock(codec, size, data)
	if err == nil && bytes.Equal(data, e.raw) {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE request_audits SET capture_state='partial',capture_reason='chunk_conflict' WHERE id=$1`, e.id)
	return err
}

type partCounts struct {
	chunks int
	bytes  int64
	minSeq int
	maxSeq int
}

func matchesManifest(manifest PartManifest, count partCounts) bool {
	return manifest.ExpectedChunks >= 0 && manifest.ExpectedBytes >= 0 &&
		manifest.ExpectedChunks == count.chunks && manifest.ExpectedBytes == count.bytes &&
		(count.chunks == 0 || count.minSeq == 0 && count.maxSeq == count.chunks-1)
}

func reconcileUpdatedRecord(ctx context.Context, tx *sql.Tx, id string, result sql.Result) error {
	updated, err := result.RowsAffected()
	if err != nil || updated == 0 {
		return err
	}
	return reconcileRecord(ctx, tx, id)
}

// A producer end marker alone never proves persistence. This runs in the same
// transaction as the terminal event and checks every stored sequence and byte.
func reconcileRecord(ctx context.Context, tx *sql.Tx, id string) error {
	var raw []byte
	var oldState string
	var oldReason sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT terminal_manifest,capture_state,capture_reason FROM request_audits WHERE id=$1 AND cleanup_state='active' FOR UPDATE`, id).Scan(&raw, &oldState, &oldReason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	manifests := make(map[string]PartManifest)
	if err = json.Unmarshal(raw, &manifests); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT part,COUNT(*),COALESCE(SUM(byte_count),0),MIN(seq),MAX(seq) FROM request_audit_payload_chunks WHERE audit_id=$1 GROUP BY part`, id)
	if err != nil {
		return err
	}
	counts := make(map[string]partCounts)
	for rows.Next() {
		var part string
		var count partCounts
		if err = rows.Scan(&part, &count.chunks, &count.bytes, &count.minSeq, &count.maxSeq); err != nil {
			break
		}
		counts[part] = count
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	state, reason := "capturing", ""
	complete := true
	for _, part := range []string{PartHeaders, PartBody, PartTrailers} {
		manifest, ok := manifests[part]
		if !ok {
			complete = false
			continue
		}
		if manifest.Reason == "deferred_encoding" && manifest.ExpectedBytes < 0 {
			manifest.ExpectedBytes = counts[part].bytes
			manifest.Reason = ""
		}
		if !matchesManifest(manifest, counts[part]) {
			manifest.Complete, manifest.Reason = false, "missing_chunks"
			manifests[part] = manifest
		}
		if !manifest.Complete {
			state, reason, complete = "partial", manifest.Reason, false
		}
	}
	if oldState == "partial" {
		state, complete = "partial", false
	}
	if oldReason.String == "chunk_conflict" {
		reason = oldReason.String
	}
	if complete {
		state = "complete"
	}
	_, err = tx.ExecContext(ctx, `UPDATE request_audits SET capture_state=$2,capture_reason=COALESCE(NULLIF($3,''),capture_reason),terminal_manifest=$4::jsonb,observed_body_bytes=$5,stored_body_bytes=$6 WHERE id=$1 AND cleanup_state='active'`, id, state, reason, string(jsonBytes(manifests)), manifests[PartBody].ExpectedBytes, counts[PartBody].bytes)
	return err
}
