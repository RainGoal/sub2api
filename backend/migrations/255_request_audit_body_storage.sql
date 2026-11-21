ALTER TABLE request_audit_payload_chunks
    ADD COLUMN IF NOT EXISTS codec smallint NOT NULL DEFAULT 0,
    DROP CONSTRAINT IF EXISTS request_audit_payload_size_check;

ALTER TABLE request_audit_payload_chunks
    ADD CONSTRAINT request_audit_payload_size_check CHECK (
        byte_count BETWEEN 0 AND 262144 AND (
            (codec = 0 AND byte_count = octet_length(payload)) OR
            (codec = 1 AND part = 'body' AND octet_length(payload) < byte_count)
        )
    ) NOT VALID;

CREATE TABLE IF NOT EXISTS request_audit_upstream_attempts (
    audit_id uuid NOT NULL REFERENCES request_audits(id) ON DELETE CASCADE,
    attempt_no integer NOT NULL CHECK (attempt_no BETWEEN 1 AND 3),
    received_at timestamptz NOT NULL,
    method text NOT NULL,
    host text NOT NULL,
    path text NOT NULL,
    account_id bigint NOT NULL,
    status_code integer NOT NULL DEFAULT 0,
    parts jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (audit_id, attempt_no)
);

CREATE TABLE IF NOT EXISTS request_audit_upstream_chunks (
    audit_id uuid NOT NULL,
    attempt_no integer NOT NULL,
    part text NOT NULL CHECK (part IN ('request_headers','request_body','response_headers','response_body')),
    seq integer NOT NULL CHECK (seq >= 0),
    payload bytea NOT NULL,
    byte_count integer NOT NULL CHECK (byte_count BETWEEN 1 AND 65536),
    codec smallint NOT NULL CHECK (codec IN (0,1)),
    PRIMARY KEY (audit_id, attempt_no, part, seq),
    FOREIGN KEY (audit_id, attempt_no) REFERENCES request_audit_upstream_attempts(audit_id, attempt_no) ON DELETE CASCADE
);
