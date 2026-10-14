package requestaudit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const banSelect = `SELECT id,api_key_id,session_id,public_reason,internal_note,source_audit_id,session_source,created_by,created_at,expires_at,revoked_by,revoked_at FROM session_bans`

func scanBan(row rowScanner) (*SessionBan, error) {
	var b SessionBan
	var note, source, sessionSource sql.NullString
	var expires, revoked sql.NullTime
	var actor sql.NullInt64
	if err := row.Scan(&b.ID, &b.APIKeyID, &b.SessionID, &b.PublicReason, &note, &source, &sessionSource, &b.CreatedBy, &b.CreatedAt, &expires, &actor, &revoked); err != nil {
		return nil, err
	}
	b.InternalNote, b.SourceAuditID, b.SessionSource = note.String, source.String, sessionSource.String
	b.State = "active"
	if expires.Valid {
		b.ExpiresAt = &expires.Time
		if !expires.Time.After(time.Now()) {
			b.State = "expired"
		}
	}
	if revoked.Valid {
		b.RevokedAt = &revoked.Time
		b.State = "revoked"
	}
	if actor.Valid {
		b.RevokedBy = &actor.Int64
	}
	return &b, nil
}

func (m *Manager) CheckBan(ctx context.Context, key int64, session string) (*SessionBan, error) {
	if m == nil || m.policyDB == nil || key <= 0 || session == "" || !m.SessionBanEnabled() {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, m.banLookupTimeout)
	defer cancel()
	b, err := scanBan(m.policyDB.QueryRowContext(ctx, banSelect+` WHERE api_key_id=$1 AND session_id=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY created_at DESC LIMIT 1`, key, session))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		m.banCheckFailures.Add(1)
		m.warn("session ban lookup failed; this request is allowed through")
	}
	return b, err
}

func (m *Manager) GetBan(ctx context.Context, id string) (*SessionBan, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, failure(400, "INVALID_BAN_ID", "Invalid session ban ID")
	}
	b, err := scanBan(m.policyDB.QueryRowContext(ctx, banSelect+` WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(404, "BAN_NOT_FOUND", "Session ban not found")
	}
	return b, err
}

func (m *Manager) ListBans(ctx context.Context, f BanFilter) (BanList, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where, args := []string{"1=1"}, []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if f.APIKeyID > 0 {
		add("api_key_id=$%d", f.APIKeyID)
	}
	if f.SessionID != "" {
		add("session_id=$%d", f.SessionID)
	}
	if f.SourceAuditID != "" {
		if _, err := uuid.Parse(f.SourceAuditID); err != nil {
			return BanList{}, failure(400, "INVALID_AUDIT_ID", "Invalid source audit ID")
		}
		add("source_audit_id=$%d", f.SourceAuditID)
	}
	switch f.State {
	case "active":
		where = append(where, "revoked_at IS NULL AND (expires_at IS NULL OR expires_at>NOW())")
	case "expired":
		where = append(where, "revoked_at IS NULL AND expires_at<=NOW()")
	case "revoked":
		where = append(where, "revoked_at IS NOT NULL")
	case "":
	default:
		return BanList{}, failure(400, "INVALID_BAN_STATE", "Invalid session ban state")
	}
	if f.Cursor != "" {
		t, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return BanList{}, failure(400, "INVALID_CURSOR", "Invalid cursor")
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(created_at,id)<($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := m.policyDB.QueryContext(ctx, banSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC,id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return BanList{}, err
	}
	defer func() { _ = rows.Close() }()
	result := BanList{Items: []*SessionBan{}}
	for rows.Next() {
		b, e := scanBan(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, b)
		if len(result.Items) > limit {
			result.HasMore = true
			result.Items = result.Items[:limit]
			break
		}
	}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		result.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return result, rows.Err()
}

func (m *Manager) CreateBan(ctx context.Context, input CreateBanInput) (*SessionBan, error) {
	input.PublicReason = strings.TrimSpace(input.PublicReason)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
	if input.PublicReason == "" || !utf8.ValidString(input.PublicReason) || !utf8.ValidString(input.InternalNote) || strings.IndexFunc(input.PublicReason, unicode.IsControl) >= 0 || utf8.RuneCountInString(input.PublicReason) > 500 || utf8.RuneCountInString(input.InternalNote) > 4000 {
		return nil, failure(400, "INVALID_BAN_REASON", "Public reason is required (max 500 characters); internal note max 4000 characters")
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return nil, failure(400, "INVALID_BAN_EXPIRY", "Ban expiration must be in the future")
	}
	if _, err := uuid.Parse(input.SourceAuditID); err != nil {
		return nil, failure(400, "INVALID_AUDIT_ID", "Invalid source audit ID")
	}
	tx, err := m.policyDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var key int64
	var session, source sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(api_key_id,0),session_id,session_source FROM request_audits WHERE id=$1 AND `+visibleRecordSQL+` FOR SHARE`, input.SourceAuditID).Scan(&key, &session, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(404, "AUDIT_NOT_FOUND", "Request audit record not found")
	}
	if err != nil {
		return nil, err
	}
	if key <= 0 || session.String == "" {
		return nil, failure(409, "BAN_IDENTITY_UNAVAILABLE", "Audit record has no stable API key/session identity")
	}
	// Serialize this exact API-key/session pair; expired bans never block renewal.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, strconv.FormatInt(key, 10)+":"+session.String); err != nil {
		return nil, err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM session_bans WHERE api_key_id=$1 AND session_id=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()) LIMIT 1`, key, session.String).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existingID != "" {
		return nil, &Error{Status: 409, Code: "SESSION_ALREADY_BANNED", Message: "An active session ban already exists", Metadata: map[string]string{"ban_id": existingID}}
	}
	id := uuid.NewString()
	b, err := scanBan(tx.QueryRowContext(ctx, `INSERT INTO session_bans(id,api_key_id,session_id,public_reason,internal_note,source_audit_id,session_source,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id,api_key_id,session_id,public_reason,internal_note,source_audit_id,session_source,created_by,created_at,expires_at,revoked_by,revoked_at`, id, key, session.String, input.PublicReason, nullString(input.InternalNote), input.SourceAuditID, source, input.CreatedBy, input.ExpiresAt))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, &Error{Status: 503, Code: "BAN_RESULT_UNKNOWN", Message: "Ban save result is unknown; query the ban ID to confirm", Metadata: map[string]string{"ban_id": id}}
	}
	return b, nil
}

func (m *Manager) RevokeBan(ctx context.Context, id string, actor int64) (*SessionBan, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, failure(400, "INVALID_BAN_ID", "Invalid session ban ID")
	}
	b, err := scanBan(m.policyDB.QueryRowContext(ctx, `UPDATE session_bans SET revoked_by=COALESCE(revoked_by,$2),revoked_at=COALESCE(revoked_at,NOW()) WHERE id=$1 RETURNING id,api_key_id,session_id,public_reason,internal_note,source_audit_id,session_source,created_by,created_at,expires_at,revoked_by,revoked_at`, id, actor))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(404, "BAN_NOT_FOUND", "Session ban not found")
	}
	if err != nil {
		return nil, &Error{Status: 503, Code: "BAN_RESULT_UNKNOWN", Message: "Ban revocation result is unknown; query the ban ID to confirm", Metadata: map[string]string{"ban_id": id}}
	}
	return b, err
}
