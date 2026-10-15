package requestaudit

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func retryable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection") || strings.Contains(s, "timeout") || strings.Contains(s, "temporarily") || strings.Contains(s, "deadlock") || strings.Contains(s, "serialization")
}

func (m *Manager) persistBatch(ctx context.Context, events []auditEvent) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	retention, err := lockRetention(ctx, tx)
	if err != nil {
		return err
	}
	for _, e := range events {
		e = storageEvent(e)
		switch e.kind {
		case "begin":
			if e.meta == nil {
				continue
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO request_audits
(id,received_at,updated_at,kind,instance_id,method,route_pattern,protocol,client_request_id,connection_id,message_seq,message_type,capture_state,capture_reason,last_event_at,cleanup_state)
SELECT $1,$2,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'capturing',NULL,$2,'active'
WHERE $2::timestamptz >= NOW() - ($12::int * INTERVAL '1 day')
ON CONFLICT(id) DO NOTHING`,
				e.id, e.meta.ReceivedAt, e.meta.Kind, safeSummaryText(m.instanceID), e.meta.Method, e.meta.RoutePattern, e.meta.Protocol,
				nullString(e.meta.ClientRequestID), nullString(e.meta.ConnectionID), e.meta.MessageSeq, nullString(e.meta.MessageType), retention)
		case "bind":
			if e.identity == nil {
				continue
			}
			_, err = tx.ExecContext(ctx, `UPDATE request_audits SET updated_at=NOW(),last_event_at=NOW(),user_id=COALESCE(NULLIF($2,0),user_id),api_key_id=COALESCE(NULLIF($3,0),api_key_id),group_id=COALESCE(NULLIF($4,0),group_id),session_id=COALESCE(NULLIF($5,''),session_id),session_source=COALESCE(NULLIF($6,''),session_source),session_resolution=COALESCE(NULLIF($7,''),session_resolution),model=COALESCE(NULLIF($8,''),model) WHERE id=$1 AND cleanup_state='active' AND received_at >= NOW() - ($9::int * INTERVAL '1 day')`, e.id, e.identity.UserID, e.identity.APIKeyID, e.identity.GroupID, e.identity.SessionID, e.identity.SessionSource, e.identity.SessionResolution, e.identity.Model, retention)
		case "chunk":
			err = persistChunk(ctx, tx, e, retention)
		case "usage_link":
			err = persistUsageLink(ctx, tx, e, retention)
		case "part_end":
			if e.manifest != nil {
				_, err = tx.ExecContext(ctx, `UPDATE request_audits SET terminal_manifest=jsonb_set(terminal_manifest,ARRAY[$2::text],$3::jsonb),updated_at=NOW(),last_event_at=NOW() WHERE id=$1 AND cleanup_state='active' AND received_at >= NOW() - ($4::int * INTERVAL '1 day')`, e.id, e.part, string(jsonBytes(e.manifest)), retention)
				if err == nil {
					err = reconcileRecord(ctx, tx, e.id)
				}
			}
		case "finish":
			if e.completion == nil {
				continue
			}
			_, err = tx.ExecContext(ctx, `UPDATE request_audits SET updated_at=NOW(),last_event_at=NOW(),user_id=COALESCE(NULLIF($2,0),user_id),api_key_id=COALESCE(NULLIF($3,0),api_key_id),group_id=COALESCE(NULLIF($4,0),group_id),session_id=COALESCE(NULLIF($5,''),session_id),session_source=COALESCE(NULLIF($6,''),session_source),session_resolution=COALESCE(NULLIF($7,''),session_resolution),model=COALESCE(NULLIF($8,''),model),http_status=$9,duration_ms=$10,outcome=$11,upstream_summary=$12,terminal_manifest=$13::jsonb,observed_body_bytes=$14,finished_at=NOW() WHERE id=$1 AND cleanup_state='active' AND received_at >= NOW() - ($15::int * INTERVAL '1 day')`,
				e.id, identityValue(e.identity, func(i Identity) int64 { return i.UserID }), identityValue(e.identity, func(i Identity) int64 { return i.APIKeyID }), identityValue(e.identity, func(i Identity) int64 { return i.GroupID }),
				identityValue(e.identity, func(i Identity) string { return i.SessionID }), identityValue(e.identity, func(i Identity) string { return i.SessionSource }), identityValue(e.identity, func(i Identity) string { return i.SessionResolution }), identityValue(e.identity, func(i Identity) string { return i.Model }),
				e.completion.HTTPStatus, e.completion.DurationMS, nullString(e.completion.Outcome), nullString(e.completion.UpstreamSummary), string(jsonBytes(e.manifests)), e.observedBodyBytes, retention)
			if err == nil {
				err = reconcileRecord(ctx, tx, e.id)
			}
		}
		if err != nil {
			return isolateEventError(e.id, err)
		}
	}
	return tx.Commit()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func identityValue[T any](id *Identity, f func(Identity) T) T {
	if id == nil {
		var z T
		return z
	}
	return f(*id)
}
func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }

func (m *Manager) loadConfig(ctx context.Context) (Config, error) {
	cfg := DefaultConfig()
	var raw string
	err := m.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, ConfigKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return DefaultConfig(), err
	}
	if cfg.Version < 1 {
		cfg.Version = 1
	}
	if err := validateConfig(cfg); err != nil {
		return DefaultConfig(), err
	}
	return cfg, nil
}

func validateConfig(cfg Config) error {
	if cfg.RetentionDays < 1 || cfg.RetentionDays > 30 {
		return failure(400, "INVALID_RETENTION_DAYS", "retention_days must be between 1 and 30")
	}
	return nil
}

func (m *Manager) GetConfig(ctx context.Context) (Config, error) {
	m.mu.RLock()
	known, cfg := m.configKnown, m.config
	m.mu.RUnlock()
	if known {
		return cfg, nil
	}
	return m.loadConfig(ctx)
}

func (m *Manager) UpdateConfig(ctx context.Context, input ConfigUpdate) (Config, error) {
	if input.RetentionDays < 1 || input.RetentionDays > 30 {
		return Config{}, failure(400, "INVALID_RETENTION_DAYS", "retention_days must be between 1 and 30")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, ConfigKey); err != nil {
		return Config{}, err
	}
	current := DefaultConfig()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, ConfigKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Config{}, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &current); err != nil {
			return Config{}, err
		}
	}
	if current.Version < 1 {
		current.Version = 1
	}
	if input.ExpectedVersion != current.Version {
		return Config{}, failure(409, "CONFIG_VERSION_CONFLICT", "configuration was updated by another administrator")
	}
	next := Config{Enabled: input.Enabled, RetentionDays: input.RetentionDays, SessionBanEnabled: input.SessionBanEnabled, Version: current.Version + 1, UpdatedAt: time.Now().UTC(), UpdatedBy: input.UpdatedBy}
	b, _ := json.Marshal(next)
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, ConfigKey, string(b)); err != nil {
		return Config{}, err
	}
	if err = tx.Commit(); err != nil {
		return Config{}, err
	}
	m.mu.Lock()
	m.config, m.configKnown = next, true
	m.mu.Unlock()
	return next, nil
}

func encodeCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id))
}
func decodeCursor(value string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", err
	}
	p := strings.SplitN(string(b), "|", 2)
	if len(p) != 2 {
		return time.Time{}, "", errors.New("invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, p[0])
	if err == nil {
		_, err = uuid.Parse(p[1])
	}
	return t, p[1], err
}

const recordSelect = `SELECT id,received_at,updated_at,kind,instance_id,method,route_pattern,protocol,client_request_id,connection_id,message_seq,message_type,user_id,api_key_id,group_id,session_id,session_source,session_resolution,model,http_status,duration_ms,outcome,upstream_summary,capture_state,capture_reason,terminal_manifest,observed_body_bytes,stored_body_bytes,finished_at,last_event_at FROM request_audits`

// Resolve display metadata after selecting the audit page. LEFT JOIN also keeps
// history readable when an association is missing or has been soft-deleted.
func withRecordMetadata(query string) string {
	return `SELECT r.*,u.username,u.email,k.name,g.platform,g.name FROM (` + query + `) r
LEFT JOIN users u ON u.id=r.user_id
LEFT JOIN api_keys k ON k.id=r.api_key_id
LEFT JOIN groups g ON g.id=r.group_id`
}

func (m *Manager) List(ctx context.Context, f ListFilter) (ListResult, error) {
	if f.RequestID != "" && !ValidRequestID(f.RequestID) {
		return ListResult{}, failure(400, "INVALID_ARGUMENT", "Invalid request_id")
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where, args := []string{visibleRecordSQL}, []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if !f.From.IsZero() {
		add("received_at >= $%d", f.From)
	}
	if !f.To.IsZero() {
		add("received_at < $%d", f.To)
	}
	if f.UserID > 0 {
		add("user_id = $%d", f.UserID)
	}
	if f.APIKeyID > 0 {
		add("api_key_id = $%d", f.APIKeyID)
	}
	if f.GroupID > 0 {
		add("group_id = $%d", f.GroupID)
	}
	if f.RequestID != "" {
		// Ordinary HTTP usage IDs add exactly one client: prefix. Other final
		// IDs (including WS response IDs) use explicit entry associations.
		// UNION keeps both lookups on equality indexes instead of an OR scan.
		args = append(args, strings.TrimPrefix(f.RequestID, "client:"), f.RequestID)
		where = append(where, `kind IN ('http_request','websocket_handshake')`, fmt.Sprintf(
			`id IN (SELECT id FROM request_audits WHERE `+visibleRecordSQL+` AND kind IN ('http_request','websocket_handshake') AND client_request_id=$%d
UNION SELECT audit_id FROM request_audit_usage_links WHERE usage_request_id=$%d)`, len(args)-1, len(args)))
	}
	if f.SessionID != "" {
		add("session_id = $%d", f.SessionID)
	}
	if f.Kind != "" {
		add("kind = $%d", f.Kind)
	}
	if f.CaptureState != "" {
		add("capture_state = $%d", f.CaptureState)
	}
	if f.HTTPStatus > 0 {
		add("http_status = $%d", f.HTTPStatus)
	}
	if f.Cursor != "" {
		t, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return ListResult{}, failure(400, "INVALID_CURSOR", "invalid cursor")
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(received_at,id) < ($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	query := recordSelect + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY received_at DESC,id DESC LIMIT $` + strconv.Itoa(len(args))
	query = withRecordMetadata(query) + ` ORDER BY r.received_at DESC,r.id DESC`
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ListResult{}, err
	}
	defer func() { _ = rows.Close() }()
	result := ListResult{Items: []*Record{}}
	for rows.Next() {
		record, e := scanRecord(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, record)
		if len(result.Items) > limit {
			result.HasMore = true
			result.Items = result.Items[:limit]
			break
		}
	}
	if len(result.Items) > 0 && result.HasMore {
		last := result.Items[len(result.Items)-1]
		result.NextCursor = encodeCursor(last.ReceivedAt, last.ID)
	}
	return result, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanRecord(row rowScanner) (*Record, error) {
	var r Record
	var client, conn, msgType, session, source, resolution, model, outcome, upstream, state, reason sql.NullString
	var username, email, keyName, platform, groupName sql.NullString
	var seq, uid, key, gid, status, duration sql.NullInt64
	var manifest []byte
	var finished sql.NullTime
	err := row.Scan(&r.ID, &r.ReceivedAt, &r.UpdatedAt, &r.Kind, &r.InstanceID, &r.Method, &r.RoutePattern, &r.Protocol, &client, &conn, &seq, &msgType, &uid, &key, &gid, &session, &source, &resolution, &model, &status, &duration, &outcome, &upstream, &state, &reason, &manifest, &r.ObservedBodyBytes, &r.StoredBodyBytes, &finished, &r.LastEventAt, &username, &email, &keyName, &platform, &groupName)
	if err != nil {
		return nil, err
	}
	r.ClientRequestID, r.ConnectionID, r.MessageType = client.String, conn.String, msgType.String
	r.MessageSeq = seq.Int64
	r.UserID, r.APIKeyID, r.GroupID = uid.Int64, key.Int64, gid.Int64
	r.UserUsername, r.UserEmail, r.APIKeyName, r.GroupPlatform = username.String, email.String, keyName.String, platform.String
	r.GroupName = groupName.String
	r.SessionID, r.SessionSource, r.SessionResolution, r.Model = session.String, source.String, resolution.String, model.String
	r.HTTPStatus, r.DurationMS, r.Outcome, r.UpstreamSummary, r.CaptureState, r.CaptureReason = int(status.Int64), duration.Int64, outcome.String, upstream.String, state.String, reason.String
	_ = json.Unmarshal(manifest, &r.TerminalManifest)
	if finished.Valid {
		r.FinishedAt = &finished.Time
	}
	return &r, nil
}

func (m *Manager) Get(ctx context.Context, id string) (*Record, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, failure(400, "INVALID_AUDIT_ID", "Invalid request audit ID")
	}
	r, err := scanRecord(m.db.QueryRowContext(ctx, withRecordMetadata(recordSelect+` WHERE id=$1 AND `+visibleRecordSQL), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(404, "AUDIT_NOT_FOUND", "Request audit record not found")
	}
	return r, err
}

func (m *Manager) ReadPayload(ctx context.Context, id, part string, offset, limit int64, allowPartial bool) (PayloadResult, error) {
	if !validPart(part) {
		return PayloadResult{}, failure(400, "INVALID_ARGUMENT", "invalid payload part")
	}
	if offset < 0 || limit < 1 || limit > MaxPayloadRead {
		return PayloadResult{}, failure(416, "INVALID_PAYLOAD_RANGE", "invalid payload range")
	}
	record, err := m.Get(ctx, id)
	if err != nil {
		return PayloadResult{}, err
	}
	manifest, ended := record.TerminalManifest[part]
	if !ended && !allowPartial {
		return PayloadResult{}, failure(409, "AUDIT_PAYLOAD_NOT_READY", "payload capture has not finished")
	}
	rows, err := m.db.QueryContext(ctx, `SELECT c.seq,c.byte_count FROM request_audits r LEFT JOIN request_audit_payload_chunks c ON c.audit_id=r.id AND c.part=$2 WHERE r.id=$1 AND `+visibleRecordSQL+` ORDER BY c.seq`, id, part)
	if err != nil {
		return PayloadResult{}, err
	}
	count, prefixChunks := partCounts{}, 0
	var prefixBytes int64
	found, prefix := false, true
	for rows.Next() {
		found = true
		var seq, size sql.NullInt64
		if err = rows.Scan(&seq, &size); err != nil {
			break
		}
		if !seq.Valid {
			continue
		}
		if count.chunks == 0 {
			count.minSeq = int(seq.Int64)
		}
		count.chunks++
		count.bytes += size.Int64
		count.maxSeq = int(seq.Int64)
		if int(seq.Int64) != prefixChunks || size.Int64 < 0 || size.Int64 > ChunkBytes {
			prefix = false
		}
		if prefix {
			prefixChunks++
			prefixBytes += size.Int64
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return PayloadResult{}, err
	}
	if !found {
		return PayloadResult{}, failure(404, "AUDIT_NOT_FOUND", "Request audit record not found")
	}
	partial := !ended || !manifest.Complete || !matchesManifest(manifest, count) || record.CaptureReason == "chunk_conflict"
	if partial && !allowPartial {
		return PayloadResult{}, failure(409, "AUDIT_PAYLOAD_INCOMPLETE", "payload is incomplete")
	}
	if offset > prefixBytes {
		return PayloadResult{}, failure(416, "INVALID_PAYLOAD_RANGE", "invalid payload range")
	}
	end := offset + min(limit, prefixBytes-offset)
	raw, err := m.readPayloadRange(ctx, id, part, prefixChunks, offset, end)
	if err != nil {
		return PayloadResult{}, err
	}
	return PayloadResult{Bytes: raw, TotalBytes: prefixBytes, HasMore: end < prefixBytes, CaptureState: record.CaptureState, Partial: partial}, nil
}

func (m *Manager) cleanupOnce(ctx context.Context) {
	if m == nil || m.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		batchCtx, batchCancel := context.WithTimeout(ctx, m.writeTimeout)
		more, err := m.cleanupBatch(batchCtx)
		batchCancel()
		if err != nil {
			m.cleanupFailures.Add(1)
			m.warn("retention cleanup failed; expired records remain hidden")
			return
		}
		m.lastCleanup.Store(time.Now().UnixNano())
		if !more {
			return
		}
	}
}

const cleanupDeletingSQL = `SELECT id FROM request_audits WHERE cleanup_state='deleting' ORDER BY received_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`
const cleanupExpiredSQL = `SELECT id FROM request_audits WHERE cleanup_state='active' AND received_at < NOW() - ($1::int * INTERVAL '1 day') ORDER BY received_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`

func (m *Manager) cleanupBatch(ctx context.Context) (bool, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	retention, err := lockRetention(ctx, tx)
	if err != nil {
		return false, err
	}
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('request_audit_cleanup'))`).Scan(&locked); err != nil || !locked {
		return false, err
	}
	var id string
	err = tx.QueryRowContext(ctx, cleanupDeletingSQL).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, cleanupExpiredSQL, retention).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE request_audits SET cleanup_state='deleting',updated_at=NOW() WHERE id=$1`, id); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM request_audit_payload_chunks WHERE (audit_id,part,seq) IN (SELECT audit_id,part,seq FROM request_audit_payload_chunks WHERE audit_id=$1 ORDER BY part,seq LIMIT 128)`, id); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM request_audits WHERE id=$1 AND cleanup_state='deleting' AND NOT EXISTS(SELECT 1 FROM request_audit_payload_chunks WHERE audit_id=$1)`, id)
	if err != nil {
		return false, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	m.cleanupDeleted.Add(deleted)
	return true, nil
}
