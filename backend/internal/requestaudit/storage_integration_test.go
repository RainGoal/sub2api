//go:build integration

package requestaudit

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRequestAuditPostgresIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("requestaudit_test"),
		tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err, "requires an isolated Docker PostgreSQL; missing Docker is not a successful integration check")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, `CREATE TABLE settings(key text PRIMARY KEY,value text NOT NULL,updated_at timestamptz NOT NULL DEFAULT NOW());
CREATE TABLE users(id bigint PRIMARY KEY,username text,email text,deleted_at timestamptz);
CREATE TABLE api_keys(id bigint PRIMARY KEY,user_id bigint,name text,deleted_at timestamptz);
CREATE TABLE groups(id bigint PRIMARY KEY,platform text,name text,deleted_at timestamptz)`)
	require.NoError(t, err)
	for _, name := range []string{"243_request_audit.sql", "244_request_audit_cleanup_index.sql", "245_request_audit_global_session_index.sql", "246_request_audit_filter_indexes.sql", "247_request_audit_device_bans.sql"} {
		migration, readErr := migrations.FS.ReadFile(name)
		require.NoError(t, readErr)
		for range 2 {
			_, err = db.ExecContext(ctx, string(migration))
			require.NoError(t, err, "migration must safely rerun")
		}
	}
	m := NewManager(db)
	m.running, m.configKnown, m.config.Enabled = true, true, true
	m.queue = make(chan auditEvent, m.maxEvents)
	create := func(body []byte) string {
		c := m.Begin(RequestMeta{Kind: "http_request", Headers: http.Header{"X-Multi": {"one", "two"}}, RequestURI: "/v1/messages?q=%2F", ContentLength: int64(len(body))})
		require.NotNil(t, c)
		c.Bind(Identity{UserID: 1, APIKeyID: 42, SessionID: uuid.NewString(), SessionSource: "header"})
		c.Append(PartBody, body)
		c.EndPart(PartBody, true, "")
		c.Finish()
		events := takeEvents(m)
		require.NoError(t, m.persistBatch(ctx, events))
		m.releaseEvents(events)
		return c.id
	}

	t.Run("display metadata retains soft deleted and missing associations", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `INSERT INTO users VALUES(101,'alice','alice@example.com',NULL);
INSERT INTO api_keys VALUES(201,101,'Production',NULL);
INSERT INTO groups VALUES(301,'composite','Engineering',NULL)`)
		require.NoError(t, err)
		olderID := create([]byte(`{"model":"older"}`))
		_, err = db.ExecContext(ctx, `UPDATE request_audits SET user_id=101,api_key_id=201,group_id=301,received_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, olderID)
		require.NoError(t, err)
		id := create([]byte(`{"model":"gpt-5"}`))
		_, err = db.ExecContext(ctx, `UPDATE request_audits SET user_id=101,api_key_id=201,group_id=301,model='gpt-5',route_pattern='/antigravity/v1/messages' WHERE id=$1`, id)
		require.NoError(t, err)
		for _, phase := range []string{"active", "soft_deleted", "missing"} {
			t.Run(phase, func(t *testing.T) {
				if phase == "soft_deleted" {
					_, err = db.ExecContext(ctx, `UPDATE users SET deleted_at=NOW(); UPDATE api_keys SET deleted_at=NOW(); UPDATE groups SET deleted_at=NOW()`)
					require.NoError(t, err)
				} else if phase == "missing" {
					_, err = db.ExecContext(ctx, `DELETE FROM users; DELETE FROM api_keys; DELETE FROM groups`)
					require.NoError(t, err)
				}
				detail, err := m.Get(ctx, id)
				require.NoError(t, err)
				list, err := m.List(ctx, ListFilter{UserID: 101, APIKeyID: 201, Limit: 1})
				require.NoError(t, err)
				require.Len(t, list.Items, 1)
				require.True(t, list.HasMore)
				require.NotEmpty(t, list.NextCursor)
				require.Equal(t, id, detail.ID)
				require.Equal(t, id, list.Items[0].ID)
				next, err := m.List(ctx, ListFilter{UserID: 101, APIKeyID: 201, Limit: 1, Cursor: list.NextCursor})
				require.NoError(t, err)
				require.Len(t, next.Items, 1)
				require.False(t, next.HasMore)
				require.Empty(t, next.NextCursor)
				require.Equal(t, olderID, next.Items[0].ID)
				for _, record := range []*Record{detail, list.Items[0], next.Items[0]} {
					require.EqualValues(t, 101, record.UserID)
					require.EqualValues(t, 201, record.APIKeyID)
					require.EqualValues(t, 301, record.GroupID)
					if phase == "missing" {
						require.Empty(t, record.UserUsername)
						require.Empty(t, record.UserEmail)
						require.Empty(t, record.APIKeyName)
						require.Empty(t, record.GroupPlatform)
						require.Empty(t, record.GroupName)
					} else {
						require.Equal(t, "alice", record.UserUsername)
						require.Equal(t, "alice@example.com", record.UserEmail)
						require.Equal(t, "Production", record.APIKeyName)
						require.Equal(t, "Engineering", record.GroupName)
						require.Equal(t, "composite", record.GroupPlatform, "do not infer request platform from model or path")
					}
				}
			})
		}
	})

	t.Run("session filter spans keys and supports key narrowing", func(t *testing.T) {
		testGlobalSessionFilter(t, ctx, db, m)
	})

	t.Run("group filter preserves retention and cursor order", func(t *testing.T) {
		testGroupFilter(t, ctx, db, m)
	})

	t.Run("usage request IDs resolve explicit entries and historical clients", func(t *testing.T) {
		testUsageRequestFilter(t, ctx, db, m)
	})

	t.Run("invalid summaries preserve raw bytes and healthy requests", func(t *testing.T) {
		body := []byte(`{"model":"bad\u0000model"}`)
		c := m.Begin(RequestMeta{Kind: "http_request"})
		require.NotNil(t, c)
		c.Bind(Identity{Model: "bad\x00model", SessionSource: string([]byte{0xff})})
		c.Append(PartBody, body)
		c.EndPart(PartBody, true, "")
		c.Finish()
		events := takeEvents(m)
		require.NoError(t, m.persistBatch(ctx, events))
		m.releaseEvents(events)
		record, err := m.Get(ctx, c.id)
		require.NoError(t, err)
		require.Empty(t, record.Model)
		require.Empty(t, record.SessionSource)
		payload, err := m.ReadPayload(ctx, c.id, PartBody, 0, MaxPayloadRead, false)
		require.NoError(t, err)
		require.Equal(t, body, payload.Bytes)

		healthy := m.Begin(RequestMeta{Kind: "http_request"})
		require.NotNil(t, healthy)
		healthy.EndPart(PartBody, true, "")
		healthy.Finish()
		require.True(t, m.enqueue(auditEvent{kind: "begin", id: "invalid-uuid", meta: &RequestMeta{ReceivedAt: time.Now(), Kind: "http_request"}}))
		m.flush(ctx, takeEvents(m))
		record, err = m.Get(ctx, healthy.id)
		require.NoError(t, err)
		require.Equal(t, "complete", record.CaptureState)
		require.Zero(t, m.circuitUntil.Load())
		require.Zero(t, m.queueBytes.Load())
	})

	t.Run("raw bytes and missing chunks", func(t *testing.T) {
		body := bytes.Repeat([]byte{0xff, 0, 1, 2}, ChunkBytes/2+7)
		id := create(body)
		record, err := m.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "complete", record.CaptureState)
		require.EqualValues(t, len(body), record.StoredBodyBytes)
		var got []byte
		for offset := int64(0); offset < int64(len(body)); {
			part, err := m.ReadPayload(ctx, id, PartBody, offset, MaxPayloadRead, false)
			require.NoError(t, err)
			require.NotEmpty(t, part.Bytes)
			got = append(got, part.Bytes...)
			offset += int64(len(part.Bytes))
		}
		require.Equal(t, body, got)
		_, err = db.ExecContext(ctx, `DELETE FROM request_audit_payload_chunks WHERE audit_id=$1 AND part='body' AND seq=1`, id)
		require.NoError(t, err)
		_, err = m.ReadPayload(ctx, id, PartBody, 0, MaxPayloadRead, false)
		require.Error(t, err)
		partial, err := m.ReadPayload(ctx, id, PartBody, 0, MaxPayloadRead, true)
		require.NoError(t, err)
		require.True(t, partial.Partial)
		require.EqualValues(t, ChunkBytes, partial.TotalBytes)
		require.Equal(t, body[:ChunkBytes], partial.Bytes)
	})

	t.Run("database retention and independent ban lifetime", func(t *testing.T) {
		id := create([]byte("retain then expire"))
		ban, err := m.CreateBan(ctx, CreateBanInput{SourceAuditID: id, PublicReason: "test reason", InternalNote: "admin only", CreatedBy: 1})
		require.NoError(t, err)
		cfg, err := m.UpdateConfig(ctx, ConfigUpdate{RetentionDays: 3, SessionBanEnabled: true, ExpectedVersion: 1, UpdatedBy: 1})
		require.NoError(t, err)
		require.False(t, m.Enabled(), "capture disabled independently from manual policy")
		_, err = db.ExecContext(ctx, `UPDATE request_audits SET received_at=NOW()-INTERVAL '72 hours'+INTERVAL '1 minute' WHERE id=$1`, id)
		require.NoError(t, err)
		_, err = m.Get(ctx, id)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `UPDATE request_audits SET received_at=NOW()-INTERVAL '72 hours'-INTERVAL '1 minute' WHERE id=$1`, id)
		require.NoError(t, err)
		_, err = m.Get(ctx, id)
		require.Error(t, err, "expired data is hidden before cleanup")
		_, err = m.ReadPayload(ctx, id, PartBody, 0, 64, true)
		require.Error(t, err)
		m.cleanupOnce(ctx)
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_audits WHERE id=$1`, id).Scan(&count))
		require.Zero(t, count)
		active, err := m.CheckBan(ctx, ban.APIKeyID, ban.SessionID)
		require.NoError(t, err)
		require.Equal(t, ban.ID, active.ID)
		_, err = m.GetBan(ctx, ban.ID)
		require.NoError(t, err)
		_, err = m.UpdateConfig(ctx, ConfigUpdate{Enabled: true, RetentionDays: 3, SessionBanEnabled: true, ExpectedVersion: cfg.Version, UpdatedBy: 1})
		require.NoError(t, err)
	})

	t.Run("cleanup is bounded and cannot resurrect", func(t *testing.T) {
		id := create([]byte("seed"))
		_, err = db.ExecContext(ctx, `DELETE FROM request_audit_payload_chunks WHERE audit_id=$1;`, id)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `INSERT INTO request_audit_payload_chunks(audit_id,part,seq,payload,byte_count) SELECT $1,'body',n,decode('61','hex'),1 FROM generate_series(0,259) n`, id)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `UPDATE request_audits SET received_at=NOW()-INTERVAL '4 days' WHERE id=$1`, id)
		require.NoError(t, err)
		more, err := m.cleanupBatch(ctx)
		require.NoError(t, err)
		require.True(t, more)
		var remaining int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_audit_payload_chunks WHERE audit_id=$1`, id).Scan(&remaining))
		require.Equal(t, 132, remaining)
		cfg, err := m.GetConfig(ctx)
		require.NoError(t, err)
		_, err = m.UpdateConfig(ctx, ConfigUpdate{Enabled: true, RetentionDays: 30, ExpectedVersion: cfg.Version, UpdatedBy: 1})
		require.NoError(t, err)
		_, err = m.Get(ctx, id)
		require.Error(t, err, "deleting record stays hidden after retention extension")
		m.cleanupOnce(ctx)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_audits WHERE id=$1`, id).Scan(&remaining))
		require.Zero(t, remaining)
	})

	t.Run("device policy spans keys and survives source retention", func(t *testing.T) {
		testDevicePolicyIntegration(t, ctx, db, m)
	})

	t.Run("cleanup branches have ordered index paths", func(t *testing.T) {
		_, err = db.ExecContext(ctx, `INSERT INTO request_audits(id,received_at,kind,instance_id,cleanup_state)
SELECT md5('cleanup-plan-'||s::text)::uuid,NOW()-(s%5)*INTERVAL '1 day','http_request','test',
CASE WHEN s%100=0 THEN 'deleting' ELSE 'active' END FROM generate_series(1,20000) s; ANALYZE request_audits`)
		require.NoError(t, err)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan=off`)
		require.NoError(t, err)
		for _, query := range []struct {
			sql  string
			args []any
		}{{cleanupDeletingSQL, nil}, {cleanupExpiredSQL, []any{3}}} {
			rows, err := tx.QueryContext(ctx, "EXPLAIN "+query.sql, query.args...)
			require.NoError(t, err)
			var lines []string
			for rows.Next() {
				var line string
				require.NoError(t, rows.Scan(&line))
				lines = append(lines, line)
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
			plan := strings.Join(lines, "\n")
			require.Contains(t, plan, "Index Scan", plan)
			require.NotContains(t, plan, "Seq Scan", plan)
			require.NotContains(t, plan, "Sort", plan)
		}
	})

	t.Run("global session page has an ordered index path", func(t *testing.T) {
		testGlobalSessionIndexPlan(t, ctx, db)
	})

	t.Run("group page has an ordered index path", func(t *testing.T) {
		testGroupIndexPlan(t, ctx, db)
	})

	t.Run("request IDs have exact indexed lookup paths", func(t *testing.T) {
		testRequestIDIndexPlans(t, ctx, db)
	})
}
