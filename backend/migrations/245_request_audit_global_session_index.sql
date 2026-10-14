-- Session-only audit search spans API keys while preserving time/cursor order.
-- The existing API-key/session index continues to serve key-scoped searches.
CREATE INDEX IF NOT EXISTS idx_request_audits_global_session_time
    ON request_audits(session_id,received_at DESC,id DESC)
    WHERE cleanup_state='active' AND session_id IS NOT NULL;
