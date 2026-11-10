-- Bind monitors by group identity; keep legacy group_name values unbound.
-- Names are resolved on reads so group renames require no monitor updates.
ALTER TABLE channel_monitors
    ADD COLUMN IF NOT EXISTS group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_channel_monitors_group_id ON channel_monitors(group_id);

COMMENT ON COLUMN channel_monitors.group_id IS
    'Bound group ID; group_name remains a legacy label for unbound monitors';
