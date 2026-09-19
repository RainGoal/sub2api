-- User-selected fallback is opt-in; existing keys preserve their routing.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS fallback_group_id BIGINT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_fallback_group_id ON api_keys (fallback_group_id);

-- Groups use soft deletion. Application deletion clears references in the same
-- transaction; runtime validation also rejects removed or disabled groups.
COMMENT ON COLUMN api_keys.fallback_group_id IS 'Optional user-selected failure fallback group';
