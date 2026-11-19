-- Preserve the existing account scheduling order when group-specific priority
-- becomes active by initializing each relation from its account priority.
WITH updated AS (
    UPDATE account_groups AS account_group
    SET priority = account.priority
    FROM accounts AS account
    WHERE account.id = account_group.account_id
      AND account_group.priority IS DISTINCT FROM account.priority
    RETURNING account_group.account_id, account_group.group_id
), events AS (
    SELECT account_id, jsonb_build_object('group_ids', array_agg(group_id)) AS payload
    FROM updated
    GROUP BY account_id
)
INSERT INTO scheduler_outbox (event_type, account_id, payload)
SELECT 'account_groups_changed', account_id, payload
FROM events;
