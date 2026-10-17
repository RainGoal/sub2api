-- Legacy task identifiers and billing fields retain their historical meaning.
-- New application instances must be deployed everywhere before enabling v1
-- creation; old recovery workers cannot interpret these snapshots.
ALTER TABLE custom_seedance_video_tasks
    ADD COLUMN IF NOT EXISTS task_id_version SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sales_snapshot JSONB,
    ADD COLUMN IF NOT EXISTS execution_snapshot JSONB,
    ADD COLUMN IF NOT EXISTS review_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS review_requested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS review_deadline TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS review_decisions JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE custom_seedance_video_tasks
    DROP CONSTRAINT IF EXISTS custom_seedance_video_tasks_duration_seconds_check,
    DROP CONSTRAINT IF EXISTS custom_seedance_video_tasks_settlement_status_check,
    DROP CONSTRAINT IF EXISTS custom_seedance_video_tasks_identity_version_check,
    DROP CONSTRAINT IF EXISTS custom_seedance_video_tasks_snapshots_check;

ALTER TABLE custom_seedance_video_tasks
    ADD CONSTRAINT custom_seedance_video_tasks_identity_version_check
        CHECK (task_id_version IN (0, 1)),
    ADD CONSTRAINT custom_seedance_video_tasks_duration_seconds_check
        CHECK ((task_id_version = 0 AND duration_seconds > 0)
            OR (task_id_version = 1 AND duration_seconds >= 0)),
    ADD CONSTRAINT custom_seedance_video_tasks_settlement_status_check
        CHECK (settlement_status IN ('pending', 'processing', 'settled', 'released', 'needs_review')),
    ADD CONSTRAINT custom_seedance_video_tasks_snapshots_check
        CHECK ((sales_snapshot IS NULL OR jsonb_typeof(sales_snapshot) = 'object')
            AND (execution_snapshot IS NULL OR jsonb_typeof(execution_snapshot) = 'object'));

-- Preserve legacy ownership lookup uniqueness while allowing independent
-- supplier accounts to return the same upstream identifier for new tasks.
CREATE UNIQUE INDEX IF NOT EXISTS custom_seedance_video_tasks_legacy_owner_task_uq
    ON custom_seedance_video_tasks (user_id, api_key_id, provider_task_id)
    WHERE task_id_version = 0 AND provider_task_id IS NOT NULL AND provider_task_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS custom_seedance_video_tasks_supplier_task_uq
    ON custom_seedance_video_tasks (account_id, provider_protocol, provider_task_id)
    WHERE task_id_version = 1 AND provider_task_id IS NOT NULL AND provider_task_id <> '';
DROP INDEX IF EXISTS custom_seedance_video_tasks_owner_task_uq;

CREATE INDEX IF NOT EXISTS custom_seedance_video_tasks_review_idx
    ON custom_seedance_video_tasks (review_deadline, id)
    WHERE settlement_status = 'needs_review';
