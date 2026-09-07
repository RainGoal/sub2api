package repository

import "context"

// Sales ownership survives program pauses. It always excludes ordinary
// recharge rebates, including admin recharges and redeem-code fulfillment.
func (r *affiliateRepository) IsSalesCustomer(ctx context.Context, userID int64) (bool, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM sales_customers WHERE user_id = $1)`, userID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, rows.Err()
	}
	var exists bool
	if err := rows.Scan(&exists); err != nil {
		return false, err
	}
	return exists, rows.Err()
}
