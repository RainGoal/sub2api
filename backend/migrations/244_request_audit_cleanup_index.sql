-- A separate predicate lets cleanup resume interrupted deletions through an
-- ordered partial index; the existing active index serves newly expired rows.
CREATE INDEX IF NOT EXISTS idx_request_audits_deleting_cleanup
    ON request_audits(received_at,id)
    WHERE cleanup_state='deleting';
