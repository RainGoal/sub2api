-- Independent, default-off gateway request audit. Payload bytes are intentionally
-- stored verbatim in PostgreSQL and must never be exposed to non-admin callers.
CREATE TABLE IF NOT EXISTS request_audits (
    id uuid PRIMARY KEY,
    received_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    kind text NOT NULL,
    instance_id text NOT NULL,
    method text NOT NULL DEFAULT '',
    route_pattern text NOT NULL DEFAULT '',
    protocol text NOT NULL DEFAULT '',
    client_request_id text,
    connection_id text,
    message_seq bigint,
    message_type text,
    user_id bigint,
    api_key_id bigint,
    group_id bigint,
    session_id text,
    session_source text,
    session_resolution text,
    model text,
    http_status integer,
    duration_ms bigint,
    outcome text,
    upstream_summary text,
    capture_state text NOT NULL DEFAULT 'capturing',
    capture_reason text,
    terminal_manifest jsonb NOT NULL DEFAULT '{}'::jsonb,
    observed_body_bytes bigint NOT NULL DEFAULT 0,
    stored_body_bytes bigint NOT NULL DEFAULT 0,
    finished_at timestamptz,
    last_event_at timestamptz NOT NULL DEFAULT now(),
    cleanup_state text NOT NULL DEFAULT 'active',
    CONSTRAINT request_audits_capture_state_check CHECK (capture_state IN ('capturing','complete','partial')),
    CONSTRAINT request_audits_cleanup_state_check CHECK (cleanup_state IN ('active','deleting'))
);

CREATE INDEX IF NOT EXISTS idx_request_audits_received_at ON request_audits(received_at DESC,id DESC) WHERE cleanup_state='active';
CREATE INDEX IF NOT EXISTS idx_request_audits_user_time ON request_audits(user_id,received_at DESC,id DESC) WHERE cleanup_state='active';
CREATE INDEX IF NOT EXISTS idx_request_audits_key_time ON request_audits(api_key_id,received_at DESC,id DESC) WHERE cleanup_state='active';
CREATE INDEX IF NOT EXISTS idx_request_audits_session_time ON request_audits(api_key_id,session_id,received_at DESC,id DESC) WHERE cleanup_state='active' AND session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_request_audits_cleanup ON request_audits(received_at,id) WHERE cleanup_state='active';

CREATE TABLE IF NOT EXISTS request_audit_payload_chunks (
    audit_id uuid NOT NULL REFERENCES request_audits(id) ON DELETE CASCADE,
    part text NOT NULL,
    seq integer NOT NULL,
    payload bytea NOT NULL,
    byte_count integer NOT NULL,
    PRIMARY KEY (audit_id,part,seq),
    CONSTRAINT request_audit_payload_part_check CHECK (part IN ('headers','body','trailers')),
    CONSTRAINT request_audit_payload_size_check CHECK (byte_count=octet_length(payload) AND byte_count<=262144 AND byte_count>=0)
);

CREATE TABLE IF NOT EXISTS session_bans (
    id uuid PRIMARY KEY,
    api_key_id bigint NOT NULL,
    session_id text NOT NULL,
    public_reason text NOT NULL,
    internal_note text,
    source_audit_id uuid,
    session_source text,
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    revoked_by bigint,
    revoked_at timestamptz,
    CONSTRAINT session_bans_nonempty_session CHECK (length(session_id)>0),
    CONSTRAINT session_bans_nonempty_reason CHECK (length(public_reason)>0),
    CONSTRAINT session_bans_valid_expiry CHECK (expires_at IS NULL OR expires_at>created_at)
);

CREATE INDEX IF NOT EXISTS idx_session_bans_lookup ON session_bans(api_key_id,session_id,created_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_session_bans_created ON session_bans(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_session_bans_source ON session_bans(source_audit_id) WHERE source_audit_id IS NOT NULL;
