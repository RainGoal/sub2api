-- Exact group filtering keeps the same retained-record and cursor order.
CREATE INDEX IF NOT EXISTS idx_request_audits_group_time
    ON request_audits(group_id,received_at DESC,id DESC)
    WHERE cleanup_state='active' AND group_id IS NOT NULL;

-- Older ordinary HTTP usage IDs use client:<client_request_id>. Preserve an
-- indexed exact path for those records as well as copied raw client IDs.
CREATE INDEX IF NOT EXISTS idx_request_audits_client_request_time
    ON request_audits(client_request_id,received_at DESC,id DESC)
    WHERE cleanup_state='active' AND client_request_id IS NOT NULL
        AND kind IN ('http_request','websocket_handshake');

-- One HTTP entry or WebSocket handshake can produce multiple usage events.
-- Keep their final stored IDs explicit; do not infer a WebSocket frame from
-- timing, session IDs, or a mutable latest-message pointer.
CREATE TABLE IF NOT EXISTS request_audit_usage_links (
    usage_request_id text NOT NULL,
    api_key_id bigint NOT NULL,
    audit_id uuid NOT NULL REFERENCES request_audits(id) ON DELETE CASCADE,
    PRIMARY KEY (usage_request_id,api_key_id,audit_id)
);
CREATE INDEX IF NOT EXISTS idx_request_audit_usage_links_audit_id
    ON request_audit_usage_links(audit_id);
