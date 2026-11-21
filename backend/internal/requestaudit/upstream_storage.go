package requestaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const activeAttemptParent = `SELECT id FROM request_audits WHERE id=$1 AND cleanup_state='active'
AND received_at >= NOW() - ($2::int * INTERVAL '1 minute') FOR SHARE`

func (m *Manager) persistAttemptEvent(ctx context.Context, tx *sql.Tx, e auditEvent, retention int) error {
	switch e.kind {
	case "attempt_begin":
		if e.attempt == nil {
			return nil
		}
		a := e.attempt
		_, err := tx.ExecContext(ctx, `INSERT INTO request_audit_upstream_attempts
(audit_id,attempt_no,received_at,method,host,path,account_id,parts)
SELECT id,$3,$4,$5,$6,$7,$8,$9::jsonb FROM (`+activeAttemptParent+`) parent
ON CONFLICT(audit_id,attempt_no) DO NOTHING`, e.id, retention, a.AttemptNo, a.ReceivedAt,
			safeSummaryText(a.Method), safeSummaryText(a.Host), safeSummaryText(a.Path), a.AccountID, string(jsonBytes(a.Parts)))
		return err
	case "attempt_status":
		if e.attempt == nil || e.attempt.StatusCode <= 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE request_audit_upstream_attempts a SET status_code=$4
FROM (`+activeAttemptParent+`) parent WHERE a.audit_id=parent.id AND a.attempt_no=$3`,
			e.id, retention, e.attemptNo, e.attempt.StatusCode)
		return err
	case "attempt_chunk":
		if !validUpstreamPart(e.part) || len(e.raw) == 0 {
			return nil
		}
		payload, err := m.sealUpstream(e)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO request_audit_upstream_chunks
(audit_id,attempt_no,part,seq,payload,byte_count,codec)
SELECT a.audit_id,a.attempt_no,$4,$5,$6,$7,$8 FROM (`+activeAttemptParent+`) parent
JOIN request_audit_upstream_attempts a ON a.audit_id=parent.id AND a.attempt_no=$3
ON CONFLICT(audit_id,attempt_no,part,seq) DO NOTHING`,
			e.id, retention, e.attemptNo, e.part, e.seq, payload, len(e.raw), eventCodec(e))
		return err
	case "attempt_end":
		if !validUpstreamPart(e.part) || e.manifest == nil {
			return nil
		}
		return persistAttemptEnd(ctx, tx, e, retention)
	}
	return nil
}

func persistAttemptEnd(ctx context.Context, tx *sql.Tx, e auditEvent, retention int) error {
	var count partCounts
	err := tx.QueryRowContext(ctx, `SELECT COUNT(c.seq),COALESCE(SUM(c.byte_count),0),
COALESCE(MIN(c.seq),0),COALESCE(MAX(c.seq),0)
FROM (`+activeAttemptParent+`) parent
JOIN request_audit_upstream_attempts a ON a.audit_id=parent.id AND a.attempt_no=$3
LEFT JOIN request_audit_upstream_chunks c ON c.audit_id=a.audit_id AND c.attempt_no=a.attempt_no AND c.part=$4
GROUP BY a.attempt_no`, e.id, retention, e.attemptNo, e.part).
		Scan(&count.chunks, &count.bytes, &count.minSeq, &count.maxSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	manifest := *e.manifest
	if manifest.ExpectedChunks < 0 {
		manifest.ExpectedChunks = count.chunks
	}
	if manifest.ExpectedBytes < 0 {
		manifest.ExpectedBytes = count.bytes
	}
	if !matchesManifest(manifest, count) && manifest.Complete {
		manifest.Complete, manifest.Reason = false, "missing_chunks"
	}
	_, err = tx.ExecContext(ctx, `UPDATE request_audit_upstream_attempts
SET parts=jsonb_set(parts,ARRAY[$3::text],$4::jsonb) WHERE audit_id=$1 AND attempt_no=$2`,
		e.id, e.attemptNo, e.part, string(jsonBytes(manifest)))
	return err
}

func (m *Manager) ListAttempts(ctx context.Context, id string) ([]AttemptRecord, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT COALESCE(a.attempt_no,0),COALESCE(a.received_at,r.received_at),
COALESCE(a.method,''),COALESCE(a.host,''),COALESCE(a.path,''),COALESCE(a.account_id,0),
COALESCE(a.status_code,0),COALESCE(a.parts,'{}')
FROM (SELECT id,received_at FROM request_audits WHERE id=$1 AND `+visibleRecordSQL+`) r
LEFT JOIN request_audit_upstream_attempts a ON a.audit_id=r.id ORDER BY a.attempt_no`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]AttemptRecord, 0)
	found := false
	for rows.Next() {
		found = true
		var a AttemptRecord
		var parts []byte
		if err := rows.Scan(&a.AttemptNo, &a.ReceivedAt, &a.Method, &a.Host, &a.Path, &a.AccountID, &a.StatusCode, &parts); err != nil {
			return nil, err
		}
		if a.AttemptNo == 0 {
			continue
		}
		if err := json.Unmarshal(parts, &a.Parts); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, failure(404, "AUDIT_NOT_FOUND", "Request audit record not found")
	}
	return items, nil
}

func (m *Manager) ReadAttemptPayload(ctx context.Context, id string, attempt int, part string, offset, limit int64, allowPartial bool) (PayloadResult, error) {
	if attempt < 1 || !validUpstreamPart(part) {
		return PayloadResult{}, failure(400, "INVALID_ARGUMENT", "invalid upstream payload part or attempt")
	}
	if offset < 0 || limit < 1 || limit > MaxPayloadRead {
		return PayloadResult{}, failure(416, "INVALID_PAYLOAD_RANGE", "invalid payload range")
	}
	rows, err := m.db.QueryContext(ctx, `SELECT a.parts->$3::text,c.seq,c.byte_count,c.codec,c.payload
FROM (SELECT id FROM request_audits WHERE id=$1 AND `+visibleRecordSQL+`) r
JOIN request_audit_upstream_attempts a ON a.audit_id=r.id AND a.attempt_no=$2
LEFT JOIN request_audit_upstream_chunks c ON c.audit_id=a.audit_id AND c.attempt_no=a.attempt_no AND c.part=$3
ORDER BY c.seq`, id, attempt, part)
	if err != nil {
		return PayloadResult{}, err
	}
	defer func() { _ = rows.Close() }()
	var manifest PartManifest
	var found, ended bool
	var count partCounts
	var prefixBytes int64
	prefix := true
	raw := make([]byte, 0)
	for rows.Next() {
		var seq, size, codec sql.NullInt32
		var rawManifest, ciphertext []byte
		if err := rows.Scan(&rawManifest, &seq, &size, &codec, &ciphertext); err != nil {
			return PayloadResult{}, err
		}
		if !found {
			found = true
			if len(rawManifest) > 0 {
				if err := json.Unmarshal(rawManifest, &manifest); err != nil {
					return PayloadResult{}, err
				}
			}
			if manifest.Reason == "not_selected" {
				return PayloadResult{}, failure(404, "AUDIT_NOT_FOUND", "upstream payload part is not selected")
			}
			ended = len(rawManifest) > 0 && manifest.Reason != "capturing"
			if !ended && !allowPartial {
				return PayloadResult{}, failure(409, "AUDIT_PAYLOAD_NOT_READY", "payload capture has not finished")
			}
		}
		if !seq.Valid {
			continue
		}
		chunkSeq, chunkSize := int(seq.Int32), int(size.Int32)
		if count.chunks == 0 {
			count.minSeq = chunkSeq
		}
		prefix = prefix && chunkSeq == count.chunks && chunkSize > 0
		count.chunks++
		count.bytes += int64(chunkSize)
		count.maxSeq = chunkSeq
		if !prefix {
			continue
		}
		blockStart := prefixBytes
		prefixBytes += int64(chunkSize)
		if offset >= prefixBytes || (blockStart > offset && blockStart-offset >= limit) {
			continue
		}
		data, err := m.openUpstream(id, attempt, part, chunkSeq, int(codec.Int32), chunkSize, ciphertext)
		if err != nil {
			return PayloadResult{}, failure(409, "AUDIT_PAYLOAD_INCOMPLETE", "upstream payload block is unavailable")
		}
		start := max(0, offset-blockStart)
		end := min(int64(chunkSize), start+limit-int64(len(raw)))
		raw = append(raw, data[start:end]...)
	}
	if err := rows.Err(); err != nil {
		return PayloadResult{}, err
	}
	if !found {
		return PayloadResult{}, failure(404, "AUDIT_NOT_FOUND", "Request audit attempt not found")
	}
	partial := !ended || !manifest.Complete || !matchesManifest(manifest, count)
	if partial && !allowPartial {
		return PayloadResult{}, failure(409, "AUDIT_PAYLOAD_INCOMPLETE", "payload is incomplete")
	}
	if offset > prefixBytes {
		return PayloadResult{}, failure(416, "INVALID_PAYLOAD_RANGE", "invalid payload range")
	}
	state := "complete"
	if partial {
		state = "partial"
		if !ended {
			state = "capturing"
		}
	}
	return PayloadResult{Bytes: raw, TotalBytes: prefixBytes, HasMore: prefixBytes-offset > int64(len(raw)), CaptureState: state, Partial: partial}, nil
}
