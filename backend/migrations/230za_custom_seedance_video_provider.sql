-- Freeze the selected video driver on every asynchronous task. This keeps
-- polling and settlement stable if an account is edited while a task runs.
ALTER TABLE custom_seedance_video_tasks
    ADD COLUMN IF NOT EXISTS provider_protocol VARCHAR(32) NOT NULL DEFAULT 'bblabu_v1';

CREATE INDEX IF NOT EXISTS custom_seedance_video_tasks_provider_idx
    ON custom_seedance_video_tasks (provider_protocol, settlement_status);
