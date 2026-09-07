package repository

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *salesRepository) RollbackRegistration(ctx context.Context, userID int64) error {
	return r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		// The user lock serializes with balance debits. Any financial history
		// makes the account ineligible for incomplete-registration cleanup.
		rows, err := client.QueryContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID)
		if err != nil {
			return err
		}
		_ = rows.Close()
		_, err = client.ExecContext(ctx, `DELETE FROM sales_customers c WHERE c.user_id=$1
AND NOT EXISTS (SELECT 1 FROM sales_commission_events e WHERE e.customer_user_id=c.user_id)
AND NOT EXISTS (SELECT 1 FROM sales_commission_ledger l WHERE l.customer_user_id=c.user_id)`, userID)
		if err != nil {
			return err
		}
		count, err := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_customers WHERE user_id=$1`, userID)
		if err != nil {
			return err
		}
		if count > 0 {
			return service.ErrSalesConflict
		}
		return nil
	})
}
