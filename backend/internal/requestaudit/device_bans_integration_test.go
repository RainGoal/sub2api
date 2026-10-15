//go:build integration

package requestaudit

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func testDevicePolicyIntegration(t *testing.T, ctx context.Context, db *sql.DB, m *Manager) {
	t.Helper()
	cfg, err := m.GetConfig(ctx)
	require.NoError(t, err)
	cfg, err = m.UpdateConfig(ctx, ConfigUpdate{Enabled: true, RetentionDays: 3, DeviceBanEnabled: true, ExpectedVersion: cfg.Version, UpdatedBy: 1})
	require.NoError(t, err)
	deviceID := "Original-" + uuid.NewString()
	sources := make([]string, 0, 2)
	for _, key := range []int64{6101, 6102} {
		ingress := BeginIngress(m, RequestMeta{Kind: "http_request"})
		ingress.BindIdentity(key, key, 0)
		require.True(t, ingress.SetDevice("codex", deviceID, "header.x-codex-installation-id", "resolved"))
		ingress.Capture().EndPart(PartBody, true, "")
		ingress.Finish(200, time.Now(), "completed", nil)
		events := takeEvents(m)
		require.NoError(t, m.persistBatch(ctx, events))
		m.releaseEvents(events)
		sources = append(sources, ingress.capture.id)
		record, err := m.Get(ctx, ingress.capture.id)
		require.NoError(t, err)
		require.Equal(t, deviceID, record.DeviceID)
		require.Equal(t, "codex", record.DeviceType)
		require.Equal(t, "header.x-codex-installation-id", record.DeviceSource)
		require.Equal(t, "resolved", record.DeviceResolution)
		require.Empty(t, record.SessionID)
	}
	listed, err := m.List(ctx, ListFilter{DeviceType: "codex", DeviceID: deviceID})
	require.NoError(t, err)
	require.Len(t, listed.Items, 2)
	narrowed, err := m.List(ctx, ListFilter{DeviceID: deviceID, APIKeyID: 6102})
	require.NoError(t, err)
	require.Len(t, narrowed.Items, 1)

	// Separate source requests must serialize one globally shared device policy.
	var wg sync.WaitGroup
	bans, failures := make([]*DeviceBan, 2), make([]error, 2)
	for n, source := range sources {
		wg.Add(1)
		go func(n int, source string) {
			defer wg.Done()
			bans[n], failures[n] = m.CreateDeviceBan(ctx, CreateDeviceBanInput{SourceAuditID: source, PublicReason: "device test", CreatedBy: 1})
		}(n, source)
	}
	wg.Wait()
	var ban *DeviceBan
	duplicates := 0
	for n, failure := range failures {
		if failure == nil {
			require.Nil(t, ban, "only one concurrent create may commit")
			ban = bans[n]
		} else {
			var auditErr *Error
			require.ErrorAs(t, failure, &auditErr)
			require.Equal(t, "DEVICE_ALREADY_BANNED", auditErr.Code)
			duplicates++
		}
	}
	require.NotNil(t, ban)
	require.Equal(t, 1, duplicates)
	for _, key := range []int64{6101, 6102} {
		ingress := BeginIngress(m, RequestMeta{})
		ingress.BindIdentity(key, key, 0)
		ingress.SetDevice("codex", deviceID, "header", "resolved")
		active, err := ingress.CheckDeviceBan(ctx)
		require.NoError(t, err)
		require.NotNil(t, active)
		require.Equal(t, ban.ID, active.ID)
		ingress.Finish(403, time.Now(), "device_banned", nil)
	}
	events := takeEvents(m)
	m.releaseEvents(events)
	active, err := m.CheckDeviceBan(ctx, "claude", deviceID)
	require.NoError(t, err)
	require.Nil(t, active, "platform namespaces are independent")

	cfg, err = m.UpdateConfig(ctx, ConfigUpdate{RetentionDays: 3, DeviceBanEnabled: true, ExpectedVersion: cfg.Version, UpdatedBy: 1})
	require.NoError(t, err)
	require.False(t, m.Enabled())
	active, err = m.CheckDeviceBan(ctx, "codex", deviceID)
	require.NoError(t, err)
	require.NotNil(t, active, "policy works while capture is disabled")
	cfg, err = m.UpdateConfig(ctx, ConfigUpdate{RetentionDays: 3, ExpectedVersion: cfg.Version, UpdatedBy: 1})
	require.NoError(t, err)
	active, err = m.CheckDeviceBan(ctx, "codex", deviceID)
	require.NoError(t, err)
	require.Nil(t, active, "device policy is independently disabled")
	cfg, err = m.UpdateConfig(ctx, ConfigUpdate{RetentionDays: 3, DeviceBanEnabled: true, ExpectedVersion: cfg.Version, UpdatedBy: 1})
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `UPDATE device_bans SET created_at=NOW()-INTERVAL '2 hours',expires_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, ban.ID)
	require.NoError(t, err)
	active, err = m.CheckDeviceBan(ctx, "codex", deviceID)
	require.NoError(t, err)
	require.Nil(t, active)
	ban, err = m.CreateDeviceBan(ctx, CreateDeviceBanInput{SourceAuditID: sources[0], PublicReason: "renewed device test", CreatedBy: 1})
	require.NoError(t, err, "expired rules must permit a fresh ban")
	revoked, err := m.RevokeDeviceBan(ctx, ban.ID, 2)
	require.NoError(t, err)
	require.Equal(t, "revoked", revoked.State)
	revoked, err = m.RevokeDeviceBan(ctx, ban.ID, 3)
	require.NoError(t, err)
	require.EqualValues(t, 2, *revoked.RevokedBy, "repeat revocation keeps original actor")
	active, err = m.CheckDeviceBan(ctx, "codex", deviceID)
	require.NoError(t, err)
	require.Nil(t, active)
	ban, err = m.CreateDeviceBan(ctx, CreateDeviceBanInput{SourceAuditID: sources[0], PublicReason: "persistent device test", CreatedBy: 1})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE request_audits SET received_at=NOW()-INTERVAL '31 days' WHERE id=$1 OR id=$2`, sources[0], sources[1])
	require.NoError(t, err)
	for range 2 {
		_, err = m.cleanupBatch(ctx)
		require.NoError(t, err)
	}
	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_audits WHERE id=$1 OR id=$2`, sources[0], sources[1]).Scan(&remaining))
	require.Zero(t, remaining)
	active, err = m.CheckDeviceBan(ctx, "codex", deviceID)
	require.NoError(t, err)
	require.NotNil(t, active)
	require.Equal(t, ban.ID, active.ID, "source cleanup cannot remove a rule")
	saved, err := m.GetDeviceBan(ctx, ban.ID)
	require.NoError(t, err)
	require.Equal(t, sources[0], saved.SourceAuditID)
}
