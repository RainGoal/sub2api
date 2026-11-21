package requestaudit

import "context"

func (m *Manager) readPayloadRange(ctx context.Context, id, part string, chunks int, offset, end int64) ([]byte, error) {
	if end == offset {
		return []byte{}, nil
	}
	// Locate intersecting blocks by original byte counts. Raw blocks can still
	// be sliced in SQL; encoded blocks are decoded individually.
	rows, err := m.db.QueryContext(ctx, `WITH positions AS (
SELECT seq,byte_count,COALESCE(SUM(byte_count) OVER (ORDER BY seq ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0)::bigint AS start_offset
FROM request_audit_payload_chunks WHERE audit_id=$1 AND part=$2 AND seq<$3)
SELECT CASE WHEN c.codec = 0 THEN substring(c.payload FROM (GREATEST($4::bigint,p.start_offset)-p.start_offset+1)::int
FOR (LEAST($5::bigint,p.start_offset+p.byte_count)-GREATEST($4::bigint,p.start_offset))::int)
ELSE c.payload END, c.codec, p.byte_count, p.start_offset
FROM positions p JOIN request_audit_payload_chunks c ON c.audit_id=$1 AND c.part=$2 AND c.seq=p.seq
JOIN request_audits r ON r.id=c.audit_id
WHERE p.start_offset<$5 AND p.start_offset+p.byte_count>$4 AND `+visibleRecordSQL+` ORDER BY p.seq`, id, part, chunks, offset, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	raw := make([]byte, 0, int(end-offset))
	for rows.Next() {
		var chunk []byte
		var codec, size int
		var start int64
		if err = rows.Scan(&chunk, &codec, &size, &start); err != nil {
			return nil, err
		}
		if codec != codecRaw {
			chunk, err = decodeBlock(codec, size, chunk)
			left, right := max(0, offset-start), min(int64(size), end-start)
			if err != nil || left >= right || right > int64(len(chunk)) {
				return nil, failure(409, "AUDIT_PAYLOAD_INCOMPLETE", "payload block is unavailable")
			}
			chunk = chunk[left:right]
		}
		if int64(len(raw)+len(chunk)) > end-offset {
			return nil, failure(409, "AUDIT_PAYLOAD_INCOMPLETE", "payload changed during read")
		}
		raw = append(raw, chunk...)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(raw)) != end-offset {
		return nil, failure(409, "AUDIT_PAYLOAD_INCOMPLETE", "payload became unavailable during read")
	}
	return raw, nil
}
