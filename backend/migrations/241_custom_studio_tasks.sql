-- Isolated, user-owned Studio history. Observed status/results are display
-- metadata and must never be used for gateway execution or billing decisions.
CREATE TABLE IF NOT EXISTS custom_studio_tasks (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind VARCHAR(8) NOT NULL CHECK (kind IN ('image', 'video')),
    task_id VARCHAR(256) NOT NULL,
    api_key_id BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    metadata JSONB NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, kind, task_id)
);

CREATE INDEX IF NOT EXISTS custom_studio_tasks_history_idx
    ON custom_studio_tasks (user_id, created_at DESC, kind, task_id);
