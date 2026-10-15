package requestaudit

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func deviceBanRows(id, source string, expiry, actor, revoked any) *sqlmock.Rows {
	return sqlmock.NewRows(strings.Split(deviceBanColumns, ",")).AddRow(id, "codex", "Original-Device", "public reason", "internal only", source, "header", 1, time.Now().Add(-time.Hour), expiry, actor, revoked)
}

func TestValidDeviceIdentityPreservesExactNamespaceAndValue(t *testing.T) {
	for _, deviceType := range []string{DeviceTypeClaude, DeviceTypeCodex} {
		for _, id := range []string{"Original-Device", "original-device", strings.Repeat("a", 255), strings.Repeat("中", 85)} {
			require.True(t, ValidDeviceIdentity(deviceType, id))
		}
		for _, id := range []string{"", " leading", "trailing ", "\tdevice", "bad\x00device", "bad\x7fdevice", string([]byte{0xff}), strings.Repeat("a", 256), strings.Repeat("中", 86)} {
			require.False(t, ValidDeviceIdentity(deviceType, id), "%q", id)
		}
	}
	require.False(t, ValidDeviceIdentity("unknown", "device"))
}

func TestDeviceBanGlobalAcrossKeysWithoutSessionOrCapture(t *testing.T) {
	m, mock := mockManager(t)
	m.configKnown, m.config.DeviceBanEnabled = true, true
	require.False(t, m.Enabled())
	banID, sourceID := uuid.NewString(), uuid.NewString()
	query := deviceBanSelect + ` WHERE device_type=$1 AND device_id=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY created_at DESC LIMIT 1`
	for _, key := range []int64{7, 8} {
		ingress := BeginIngress(m, RequestMeta{})
		require.True(t, ingress.SetDevice("codex", "Original-Device", "header", "resolved"))
		ingress.BindIdentity(key, key, 1)
		require.Empty(t, ingress.Identity().SessionID)
		mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs("codex", "Original-Device").WillReturnRows(deviceBanRows(banID, sourceID, nil, nil, nil))
		ban, err := ingress.CheckDeviceBan(context.Background())
		require.NoError(t, err)
		require.Equal(t, banID, ban.ID)
	}
	for _, identity := range []struct{ kind, id string }{{"claude", "Original-Device"}, {"codex", "original-device"}} {
		mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(identity.kind, identity.id).WillReturnError(sql.ErrNoRows)
		ban, err := m.CheckDeviceBan(context.Background(), identity.kind, identity.id)
		require.NoError(t, err)
		require.Nil(t, ban, "type and ID must match exactly")
	}
}

func TestDeviceBanDisabledUnknownUnauthenticatedAndMalformedSkipLookup(t *testing.T) {
	m, _ := mockManager(t)
	require.False(t, DefaultConfig().DeviceBanEnabled)
	for _, known := range []bool{false, true} {
		m.configKnown = known
		ban, err := m.CheckDeviceBan(context.Background(), "codex", "Original-Device")
		require.NoError(t, err)
		require.Nil(t, ban)
	}
	m.config.DeviceBanEnabled = true
	ingress := BeginIngress(m, RequestMeta{})
	ingress.SetDevice("codex", "Original-Device", "header", "resolved")
	ban, err := ingress.CheckDeviceBan(context.Background())
	require.NoError(t, err)
	require.Nil(t, ban, "only authenticated gateway traffic is checked")
	for _, id := range []string{"", " bad", "bad\x00id"} {
		ban, err := m.CheckDeviceBan(context.Background(), "codex", id)
		require.NoError(t, err)
		require.Nil(t, ban)
	}
}

func TestIngressKeepsFirstValidDeviceAndRefusesFinishedUpdates(t *testing.T) {
	m := captureTestManager()
	ingress := BeginIngress(m, RequestMeta{})
	require.True(t, ingress.SetDevice("codex", "bad\x00id", "header", "invalid"))
	require.Empty(t, ingress.Identity().DeviceID)
	require.True(t, ingress.SetDevice("codex", "Original-Device", "body", "resolved"))
	require.False(t, ingress.SetDevice("codex", "other", "header", "resolved"))
	require.False(t, ingress.SetDevice("codex", "", "header", "missing"))
	require.Equal(t, "Original-Device", ingress.Identity().DeviceID)
	require.Equal(t, "body", ingress.Identity().DeviceSource)
	ingress.Finish(200, time.Now(), "completed", nil)
	require.False(t, ingress.SetDevice("codex", "new", "header", "resolved"))
}

func TestDeviceTrackingGateDoesNotReplaceIdentityAcrossPolicyChanges(t *testing.T) {
	var absent *Ingress
	require.False(t, absent.DeviceTrackingEnabled())
	m := captureTestManager()
	captured := BeginIngress(m, RequestMeta{})
	require.True(t, captured.DeviceTrackingEnabled(), "capture needs the device summary without enabling policy")
	m.config.Enabled = false
	ingress := BeginIngress(m, RequestMeta{})
	require.False(t, ingress.DeviceTrackingEnabled())
	m.config.SessionBanEnabled = true
	require.False(t, ingress.DeviceTrackingEnabled(), "session policy must not enable new device parsing")
	m.config.DeviceBanEnabled = true
	require.True(t, ingress.DeviceTrackingEnabled())
	ingress.SetDevice("codex", "Original-Device", "body", "resolved")
	m.config.DeviceBanEnabled = false
	require.False(t, ingress.DeviceTrackingEnabled())
	m.config.DeviceBanEnabled = true
	require.True(t, ingress.DeviceTrackingEnabled())
	require.False(t, ingress.SetDevice("codex", "other", "body", "resolved"))
	require.Equal(t, "Original-Device", ingress.Identity().DeviceID)
}

func TestDeviceBanLookupFailureCountsAndReturnsNoPolicy(t *testing.T) {
	m, mock := mockManager(t)
	m.configKnown, m.config.DeviceBanEnabled = true, true
	mock.ExpectQuery(regexp.QuoteMeta(deviceBanSelect)).WithArgs("codex", "Original-Device").WillReturnError(errors.New("database unavailable"))
	ban, err := m.CheckDeviceBan(context.Background(), "codex", "Original-Device")
	require.Error(t, err)
	require.Nil(t, ban)
	require.EqualValues(t, 1, m.banCheckFailures.Load())
}

func expectDeviceSource(mock sqlmock.Sqlmock, sourceID, kind, deviceID string) {
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT device_type,device_id,device_source FROM request_audits WHERE id=$1 AND ` + visibleRecordSQL + ` FOR SHARE`)).WithArgs(sourceID).
		WillReturnRows(sqlmock.NewRows([]string{"device_type", "device_id", "device_source"}).AddRow(kind, deviceID, "header"))
}

func TestCreateDeviceBanUsesAuditIdentityAndSerializesGlobalPair(t *testing.T) {
	for _, commitUnknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "unknown"}[commitUnknown], func(t *testing.T) {
			m, mock := mockManager(t)
			sourceID := uuid.NewString()
			expectDeviceSource(mock, sourceID, "codex", "Original-Device")
			mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs("device:codex:Original-Device").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`SELECT id FROM device_bans`).WithArgs("codex", "Original-Device").WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery(`INSERT INTO device_bans`).WithArgs(sqlmock.AnyArg(), "codex", "Original-Device", "public reason", "internal only", sourceID, "header", int64(4), nil).
				WillReturnRows(deviceBanRows(uuid.NewString(), sourceID, nil, nil, nil))
			if commitUnknown {
				mock.ExpectCommit().WillReturnError(errors.New("connection lost during commit"))
			} else {
				mock.ExpectCommit()
			}
			ban, err := m.CreateDeviceBan(context.Background(), CreateDeviceBanInput{SourceAuditID: sourceID, PublicReason: " public reason ", InternalNote: "internal only", CreatedBy: 4})
			if commitUnknown {
				var auditErr *Error
				require.ErrorAs(t, err, &auditErr)
				require.Equal(t, "BAN_RESULT_UNKNOWN", auditErr.Code)
				_, parseErr := uuid.Parse(auditErr.Metadata["ban_id"])
				require.NoError(t, parseErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, "Original-Device", ban.DeviceID)
			}
		})
	}
}

func TestCreateDeviceBanMissingIdentityAndDuplicate(t *testing.T) {
	for _, id := range []string{"", "bad\x00id", "Original-Device"} {
		t.Run(id, func(t *testing.T) {
			m, mock := mockManager(t)
			sourceID, existing := uuid.NewString(), uuid.NewString()
			expectDeviceSource(mock, sourceID, "codex", id)
			code := "BAN_IDENTITY_UNAVAILABLE"
			if id == "Original-Device" {
				mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs("device:codex:Original-Device").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`SELECT id FROM device_bans`).WithArgs("codex", id).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(existing))
				code = "DEVICE_ALREADY_BANNED"
			}
			mock.ExpectRollback()
			_, err := m.CreateDeviceBan(context.Background(), CreateDeviceBanInput{SourceAuditID: sourceID, PublicReason: "reason"})
			var auditErr *Error
			require.ErrorAs(t, err, &auditErr)
			require.Equal(t, code, auditErr.Code)
			if code == "DEVICE_ALREADY_BANNED" {
				require.Equal(t, existing, auditErr.Metadata["ban_id"])
			}
		})
	}
}

func TestDeviceBanReadAndRevocationState(t *testing.T) {
	m, mock := mockManager(t)
	id, sourceID := uuid.NewString(), uuid.NewString()
	expiry, revoked := time.Now().Add(-time.Minute), time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(deviceBanSelect + ` WHERE id=$1`)).WithArgs(id).WillReturnRows(deviceBanRows(id, sourceID, expiry, nil, nil))
	ban, err := m.GetDeviceBan(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "expired", ban.State)
	require.Equal(t, expiry, *ban.ExpiresAt)
	mock.ExpectQuery(`UPDATE device_bans SET revoked_by=COALESCE`).WithArgs(id, int64(4)).WillReturnRows(deviceBanRows(id, sourceID, nil, int64(4), revoked))
	ban, err = m.RevokeDeviceBan(context.Background(), id, 4)
	require.NoError(t, err)
	require.Equal(t, "revoked", ban.State)
	require.EqualValues(t, 4, *ban.RevokedBy)
	mock.ExpectQuery(`UPDATE device_bans SET revoked_by=COALESCE`).WithArgs(id, int64(4)).WillReturnError(errors.New("network timeout"))
	_, err = m.RevokeDeviceBan(context.Background(), id, 4)
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "BAN_RESULT_UNKNOWN", auditErr.Code)
	require.Equal(t, id, auditErr.Metadata["ban_id"])
}

func TestDeviceBanListFiltersAndCursor(t *testing.T) {
	m, mock := mockManager(t)
	sourceID, id := uuid.NewString(), uuid.NewString()
	cursorTime := time.Now().UTC()
	query := deviceBanSelect + ` WHERE 1=1 AND device_type=$1 AND device_id=$2 AND source_audit_id=$3 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()) AND (created_at,id)<($4,$5) ORDER BY created_at DESC,id DESC LIMIT $6`
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs("codex", "Original-Device", sourceID, cursorTime, id, 2).
		WillReturnRows(deviceBanRows(id, sourceID, nil, nil, nil).AddRow(uuid.NewString(), "codex", "Original-Device", "reason", nil, sourceID, "header", 1, time.Now(), nil, nil, nil))
	result, err := m.ListDeviceBans(context.Background(), DeviceBanFilter{DeviceType: "codex", DeviceID: "Original-Device", SourceAuditID: sourceID, State: "active", Cursor: encodeCursor(cursorTime, id), Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.True(t, result.HasMore)
	require.NotEmpty(t, result.NextCursor)
}

func TestDeviceFiltersValidateBeforeDatabaseAndQueryExactIDs(t *testing.T) {
	m, mock := mockManager(t)
	for _, f := range []ListFilter{{DeviceType: "unknown"}, {DeviceID: "bad\nvalue"}, {DeviceID: " padded"}} {
		_, err := m.List(context.Background(), f)
		require.Error(t, err)
		_, err = m.ListDeviceBans(context.Background(), DeviceBanFilter{DeviceType: f.DeviceType, DeviceID: f.DeviceID})
		require.Error(t, err)
	}
	id := uuid.NewString()
	query := recordSelect + ` WHERE ` + visibleRecordSQL + ` AND device_type = $1 AND device_id = $2 ORDER BY received_at DESC,id DESC LIMIT $3`
	mock.ExpectQuery(regexp.QuoteMeta(withRecordMetadata(query)+` ORDER BY r.received_at DESC,r.id DESC`)).WithArgs("codex", "Original-Device", 51).WillReturnRows(recordRows(id, "complete", "", nil))
	result, err := m.List(context.Background(), ListFilter{DeviceType: "codex", DeviceID: "Original-Device"})
	require.NoError(t, err)
	require.Equal(t, "Original-Device", result.Items[0].DeviceID)
}

func TestPersistBindRetainsExactDeviceSummary(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	mock.ExpectBegin()
	expectRetention(mock)
	mock.ExpectExec(`UPDATE request_audits SET updated_at=.*device_type=COALESCE.*device_id=COALESCE.*device_source=COALESCE.*device_resolution=COALESCE`).
		WithArgs(id, int64(1), int64(2), int64(3), "", "", "", "model", "codex", "Original-Device", "header", "resolved", 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, m.persistBatch(context.Background(), []auditEvent{{kind: "bind", id: id, identity: &Identity{UserID: 1, APIKeyID: 2, GroupID: 3, Model: "model", DeviceType: "codex", DeviceID: "Original-Device", DeviceSource: "header", DeviceResolution: "resolved"}}}))
}
