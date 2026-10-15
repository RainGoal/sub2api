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

const deviceBanColumns = `id,device_type,device_id,public_reason,internal_note,source_audit_id,device_source,created_by,created_at,expires_at,revoked_by,revoked_at`
const deviceBanSelect = `SELECT ` + deviceBanColumns + ` FROM device_bans`

// ValidDeviceIdentity validates bounded client identifiers without normalizing
// them into another device. These values identify installations, not hardware.
func ValidDeviceIdentity(deviceType, deviceID string) bool {
	return (deviceType == DeviceTypeClaude || deviceType == DeviceTypeCodex) && validDeviceID(deviceID)
}

func validDeviceID(id string) bool {
	return id != "" && len(id) <= 255 && utf8.ValidString(id) && strings.TrimSpace(id) == id && strings.IndexFunc(id, unicode.IsControl) < 0
}

func validateDeviceFilter(deviceType, deviceID string) error {
	if deviceType != "" && deviceType != DeviceTypeClaude && deviceType != DeviceTypeCodex {
		return failure(400, "INVALID_ARGUMENT", "Invalid device_type")
	}
	if deviceID != "" && !validDeviceID(deviceID) {
		return failure(400, "INVALID_ARGUMENT", "Invalid device_id")
	}
	return nil
}

func scanDeviceBan(row rowScanner) (*DeviceBan, error) {
	var b DeviceBan
	var note, source, deviceSource sql.NullString
	var expires, revoked sql.NullTime
	var actor sql.NullInt64
	if err := row.Scan(&b.ID, &b.DeviceType, &b.DeviceID, &b.PublicReason, &note, &source, &deviceSource, &b.CreatedBy, &b.CreatedAt, &expires, &actor, &revoked); err != nil {
		return nil, err
	}
	b.InternalNote, b.SourceAuditID, b.DeviceSource = note.String, source.String, deviceSource.String
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

func (m *Manager) CheckDeviceBan(ctx context.Context, deviceType, deviceID string) (*DeviceBan, error) {
	if m == nil || m.policyDB == nil || !m.DeviceBanEnabled() || !ValidDeviceIdentity(deviceType, deviceID) {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, m.banLookupTimeout)
	defer cancel()
	b, err := scanDeviceBan(m.policyDB.QueryRowContext(ctx, deviceBanSelect+` WHERE device_type=$1 AND device_id=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY created_at DESC LIMIT 1`, deviceType, deviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		m.banCheckFailures.Add(1)
		m.warn("device ban lookup failed; this request is allowed through")
	}
	return b, err
}

func (m *Manager) GetDeviceBan(ctx context.Context, id string) (*DeviceBan, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, failure(400, "INVALID_BAN_ID", "Invalid device ban ID")
	}
	b, err := scanDeviceBan(m.policyDB.QueryRowContext(ctx, deviceBanSelect+` WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(404, "BAN_NOT_FOUND", "Device ban not found")
	}
	return b, err
}

func (m *Manager) ListDeviceBans(ctx context.Context, f DeviceBanFilter) (DeviceBanList, error) {
	if err := validateDeviceFilter(f.DeviceType, f.DeviceID); err != nil {
		return DeviceBanList{}, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where, args := []string{"1=1"}, []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if f.DeviceType != "" {
		add("device_type=$%d", f.DeviceType)
	}
	if f.DeviceID != "" {
		add("device_id=$%d", f.DeviceID)
	}
	if f.SourceAuditID != "" {
		if _, err := uuid.Parse(f.SourceAuditID); err != nil {
			return DeviceBanList{}, failure(400, "INVALID_AUDIT_ID", "Invalid source audit ID")
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
		return DeviceBanList{}, failure(400, "INVALID_BAN_STATE", "Invalid device ban state")
	}
	if f.Cursor != "" {
		t, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return DeviceBanList{}, failure(400, "INVALID_CURSOR", "Invalid cursor")
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(created_at,id)<($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := m.policyDB.QueryContext(ctx, deviceBanSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC,id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return DeviceBanList{}, err
	}
	defer func() { _ = rows.Close() }()
	result := DeviceBanList{Items: []*DeviceBan{}}
	for rows.Next() {
		b, e := scanDeviceBan(rows)
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

func (m *Manager) CreateDeviceBan(ctx context.Context, input CreateDeviceBanInput) (*DeviceBan, error) {
	input.PublicReason, input.InternalNote = strings.TrimSpace(input.PublicReason), strings.TrimSpace(input.InternalNote)
	if input.PublicReason == "" || !validSummaryText(input.PublicReason) || !validSummaryText(input.InternalNote) || strings.IndexFunc(input.PublicReason, unicode.IsControl) >= 0 || utf8.RuneCountInString(input.PublicReason) > 500 || utf8.RuneCountInString(input.InternalNote) > 4000 {
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
	var deviceType, deviceID, source sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT device_type,device_id,device_source FROM request_audits WHERE id=$1 AND `+visibleRecordSQL+` FOR SHARE`, input.SourceAuditID).Scan(&deviceType, &deviceID, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(404, "AUDIT_NOT_FOUND", "Request audit record not found")
	}
	if err != nil {
		return nil, err
	}
	if !ValidDeviceIdentity(deviceType.String, deviceID.String) {
		return nil, failure(409, "BAN_IDENTITY_UNAVAILABLE", "Audit record has no valid client device identity")
	}
	// Deliberately omit API key/user scope: the same installation is blocked
	// across credentials. The lock serializes creation without blocking renewal.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "device:"+deviceType.String+":"+deviceID.String); err != nil {
		return nil, err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM device_bans WHERE device_type=$1 AND device_id=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()) LIMIT 1`, deviceType.String, deviceID.String).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existingID != "" {
		return nil, &Error{Status: 409, Code: "DEVICE_ALREADY_BANNED", Message: "An active device ban already exists", Metadata: map[string]string{"ban_id": existingID}}
	}
	id := uuid.NewString()
	b, err := scanDeviceBan(tx.QueryRowContext(ctx, `INSERT INTO device_bans(id,device_type,device_id,public_reason,internal_note,source_audit_id,device_source,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+deviceBanColumns, id, deviceType.String, deviceID.String, input.PublicReason, nullString(input.InternalNote), input.SourceAuditID, source, input.CreatedBy, input.ExpiresAt))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, &Error{Status: 503, Code: "BAN_RESULT_UNKNOWN", Message: "Ban save result is unknown; query the ban ID to confirm", Metadata: map[string]string{"ban_id": id}}
	}
	return b, nil
}

func (m *Manager) RevokeDeviceBan(ctx context.Context, id string, actor int64) (*DeviceBan, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, failure(400, "INVALID_BAN_ID", "Invalid device ban ID")
	}
	b, err := scanDeviceBan(m.policyDB.QueryRowContext(ctx, `UPDATE device_bans SET revoked_by=COALESCE(revoked_by,$2),revoked_at=COALESCE(revoked_at,NOW()) WHERE id=$1 RETURNING `+deviceBanColumns, id, actor))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(404, "BAN_NOT_FOUND", "Device ban not found")
	}
	if err != nil {
		return nil, &Error{Status: 503, Code: "BAN_RESULT_UNKNOWN", Message: "Ban revocation result is unknown; query the ban ID to confirm", Metadata: map[string]string{"ban_id": id}}
	}
	return b, nil
}
