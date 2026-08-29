//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestConversationAuditMigrationCreatesPartitionedStorage(t *testing.T) {
	ctx := context.Background()
	var relkind string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT relkind::text FROM pg_class WHERE oid = 'conversation_audit_records'::regclass
	`).Scan(&relkind))
	require.Equal(t, "p", relkind)

	var primaryKey string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = 'conversation_audit_records'::regclass AND contype = 'p'
	`).Scan(&primaryKey))
	require.Equal(t, "PRIMARY KEY (created_at, audit_id)", primaryKey)

	var foreignKeys int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pg_constraint
		WHERE conrelid = 'conversation_audit_records'::regclass AND contype = 'f'
	`).Scan(&foreignKeys))
	require.Zero(t, foreignKeys)

	for offset := 0; offset < 3; offset++ {
		day := time.Now().UTC().AddDate(0, 0, offset)
		partitionName := "conversation_audit_records_" + day.Format("20060102")
		var exists bool
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT to_regclass($1) IS NOT NULL
		`, partitionName).Scan(&exists))
		require.True(t, exists, "missing partition %s", partitionName)

		var indexCount int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM pg_indexes WHERE tablename = $1
		`, partitionName).Scan(&indexCount))
		require.GreaterOrEqual(t, indexCount, 9, "partition must have PK plus approved filter indexes")
	}
}

func TestConversationAuditMigrationSupportsCompositeUpsertAndTombstone(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	createdAt := time.Now().UTC()
	auditID := uuid.New()
	insert := `
		INSERT INTO conversation_audit_records (
			audit_id, created_at, owner_instance_id, request_id, user_id, api_key_id,
			protocol, inbound_endpoint, record_state, capture_status
		) VALUES ($1,$2,'test-owner','request-1',1,2,'openai','/v1/responses','capturing','metadata_only')
		ON CONFLICT (created_at, audit_id) DO UPDATE SET updated_at = NOW()`
	_, err = tx.ExecContext(ctx, insert, auditID, createdAt)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, insert, auditID, createdAt)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO conversation_audit_delete_tombstones (created_at, audit_id)
		VALUES ($1, $2)
	`, createdAt, auditID)
	require.NoError(t, err)

	var payloadType string
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_name = 'conversation_audit_records' AND column_name = 'request_payload'
	`).Scan(&payloadType))
	require.Equal(t, "bytea", payloadType, fmt.Sprintf("unexpected payload type %q", payloadType))
}
