-- Isolated storage for optional conversation auditing. Payloads are compressed
-- and encrypted by the application before they reach these BYTEA columns.
CREATE TABLE IF NOT EXISTS conversation_audit_records (
    audit_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    mutable_until TIMESTAMPTZ,
    owner_instance_id VARCHAR(128) NOT NULL,
    lease_expires_at TIMESTAMPTZ,

    request_id VARCHAR(128) NOT NULL,
    session_id VARCHAR(256),
    user_id BIGINT NOT NULL,
    user_name VARCHAR(128) NOT NULL DEFAULT '',
    api_key_id BIGINT NOT NULL,
    api_key_name VARCHAR(128) NOT NULL DEFAULT '',
    group_id BIGINT,
    group_name VARCHAR(128) NOT NULL DEFAULT '',
    account_id BIGINT,
    account_name VARCHAR(128) NOT NULL DEFAULT '',

    protocol VARCHAR(64) NOT NULL,
    inbound_endpoint VARCHAR(256) NOT NULL,
    requested_model VARCHAR(256) NOT NULL DEFAULT '',
    effective_model VARCHAR(256) NOT NULL DEFAULT '',
    transport_mode VARCHAR(32) NOT NULL DEFAULT 'http',
    http_status SMALLINT,
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    record_state VARCHAR(16) NOT NULL DEFAULT 'capturing'
        CHECK (record_state IN ('capturing', 'finalized')),
    outcome_status VARCHAR(16)
        CHECK (outcome_status IS NULL OR outcome_status IN
            ('completed', 'error', 'timeout', 'partial', 'cancelled', 'unknown')),
    capture_status VARCHAR(16) NOT NULL DEFAULT 'metadata_only'
        CHECK (capture_status IN ('complete', 'truncated', 'metadata_only', 'degraded')),
    degraded_reason VARCHAR(128) NOT NULL DEFAULT '',

    request_original_bytes BIGINT NOT NULL DEFAULT 0 CHECK (request_original_bytes >= 0),
    request_stored_bytes BIGINT NOT NULL DEFAULT 0 CHECK (request_stored_bytes >= 0),
    request_compressed_bytes BIGINT NOT NULL DEFAULT 0 CHECK (request_compressed_bytes >= 0),
    request_encrypted_bytes BIGINT NOT NULL DEFAULT 0 CHECK (request_encrypted_bytes >= 0),
    request_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    request_omitted_messages INTEGER NOT NULL DEFAULT 0 CHECK (request_omitted_messages >= 0),
    request_omitted_bytes BIGINT NOT NULL DEFAULT 0 CHECK (request_omitted_bytes >= 0),
    request_codec_version SMALLINT,
    request_key_id VARCHAR(64),
    request_payload BYTEA,

    response_original_bytes BIGINT NOT NULL DEFAULT 0 CHECK (response_original_bytes >= 0),
    response_stored_bytes BIGINT NOT NULL DEFAULT 0 CHECK (response_stored_bytes >= 0),
    response_compressed_bytes BIGINT NOT NULL DEFAULT 0 CHECK (response_compressed_bytes >= 0),
    response_encrypted_bytes BIGINT NOT NULL DEFAULT 0 CHECK (response_encrypted_bytes >= 0),
    response_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    response_omitted_messages INTEGER NOT NULL DEFAULT 0 CHECK (response_omitted_messages >= 0),
    response_omitted_bytes BIGINT NOT NULL DEFAULT 0 CHECK (response_omitted_bytes >= 0),
    response_codec_version SMALLINT,
    response_key_id VARCHAR(64),
    response_payload BYTEA,

    PRIMARY KEY (created_at, audit_id),
    CHECK (
        (record_state = 'capturing' AND completed_at IS NULL AND outcome_status IS NULL)
        OR
        (record_state = 'finalized' AND completed_at IS NOT NULL AND outcome_status IS NOT NULL)
    )
) PARTITION BY RANGE (created_at);

CREATE TABLE IF NOT EXISTS conversation_audit_delete_tombstones (
    created_at TIMESTAMPTZ NOT NULL,
    audit_id UUID NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (created_at, audit_id)
);

-- Runtime partition maintenance calls this function under its own advisory lock.
-- There is deliberately no DEFAULT partition: a missing partition degrades audit
-- persistence instead of allowing unbounded accumulation in one table.
CREATE OR REPLACE FUNCTION conversation_audit_create_daily_partition(p_day DATE)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    partition_name TEXT := 'conversation_audit_records_' || TO_CHAR(p_day, 'YYYYMMDD');
    index_prefix TEXT := 'ca_' || TO_CHAR(p_day, 'YYYYMMDD');
    partition_start TIMESTAMPTZ := p_day::TIMESTAMP AT TIME ZONE 'UTC';
    partition_end TIMESTAMPTZ := (p_day + 1)::TIMESTAMP AT TIME ZONE 'UTC';
BEGIN
    EXECUTE FORMAT(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF conversation_audit_records FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        partition_start,
        partition_end
    );
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (created_at DESC, audit_id DESC)', index_prefix || '_created_idx', partition_name);
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (session_id, created_at DESC) WHERE session_id IS NOT NULL', index_prefix || '_session_idx', partition_name);
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (request_id)', index_prefix || '_request_idx', partition_name);
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (user_id, created_at DESC)', index_prefix || '_user_idx', partition_name);
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (api_key_id, created_at DESC)', index_prefix || '_key_idx', partition_name);
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (group_id, created_at DESC)', index_prefix || '_group_idx', partition_name);
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (outcome_status, created_at DESC)', index_prefix || '_outcome_idx', partition_name);
    EXECUTE FORMAT('CREATE INDEX IF NOT EXISTS %I ON %I (capture_status, created_at DESC)', index_prefix || '_capture_idx', partition_name);
END;
$$;

SELECT conversation_audit_create_daily_partition((CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::DATE);
SELECT conversation_audit_create_daily_partition(((CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::DATE + 1));
SELECT conversation_audit_create_daily_partition(((CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::DATE + 2));
