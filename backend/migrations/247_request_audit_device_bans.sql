-- Device policies use original inbound installation identifiers, independently
-- of request capture, session policy and upstream identity rewriting.
ALTER TABLE request_audits ADD COLUMN IF NOT EXISTS device_type text;
ALTER TABLE request_audits ADD COLUMN IF NOT EXISTS device_id text;
ALTER TABLE request_audits ADD COLUMN IF NOT EXISTS device_source text;
ALTER TABLE request_audits ADD COLUMN IF NOT EXISTS device_resolution text;

CREATE INDEX IF NOT EXISTS idx_request_audits_device_time
    ON request_audits(device_type,device_id,received_at DESC,id DESC)
    WHERE cleanup_state='active' AND device_id IS NOT NULL;

-- No FK to the source request: retention must never remove an active policy.
CREATE TABLE IF NOT EXISTS device_bans (
    id uuid PRIMARY KEY,
    device_type text NOT NULL,
    device_id text NOT NULL,
    public_reason text NOT NULL,
    internal_note text,
    source_audit_id uuid,
    device_source text,
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    revoked_by bigint,
    revoked_at timestamptz,
    CONSTRAINT device_bans_type CHECK (device_type IN ('claude','codex')),
    CONSTRAINT device_bans_nonempty_device CHECK (octet_length(device_id) BETWEEN 1 AND 255),
    CONSTRAINT device_bans_nonempty_reason CHECK (length(public_reason)>0),
    CONSTRAINT device_bans_valid_expiry CHECK (expires_at IS NULL OR expires_at>created_at)
);

CREATE INDEX IF NOT EXISTS idx_device_bans_lookup
    ON device_bans(device_type,device_id,created_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_device_bans_created ON device_bans(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_device_bans_source
    ON device_bans(source_audit_id) WHERE source_audit_id IS NOT NULL;
