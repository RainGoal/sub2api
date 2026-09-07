package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

// appendSalesBillingEvent is part of the debit transaction. A shared partner
// lock allows concurrent usage while serializing against policy and settlement
// changes; the background ledger worker never participates in gateway latency.
func appendSalesBillingEvent(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	if cmd == nil || cmd.BillingType != service.BillingTypeBalance || cmd.BalanceCost <= 0 {
		return nil
	}
	at := cmd.SalesConsumedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var partnerID, ruleID int64
	var rate string
	err := tx.QueryRowContext(ctx, `
SELECT p.id, r.id, r.commission_rate::text
FROM sales_customers c
JOIN sales_partners p ON p.id = c.partner_id
JOIN settings s ON s.key = 'custom_sales_config'
JOIN LATERAL (
    SELECT id, commission_rate FROM sales_commission_rules
    WHERE partner_id = p.id AND effective_at <= $2
    ORDER BY effective_at DESC, id DESC LIMIT 1
) r ON true
WHERE c.user_id = $1 AND p.accrual_enabled
  AND (s.value::jsonb ->> 'enabled') = 'true'
FOR SHARE OF p`, cmd.UserID, at).Scan(&partnerID, &ruleID, &rate)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve sales billing policy: %w", err)
	}
	if cmd.SalesCost == nil {
		return fmt.Errorf("sales billing cost snapshot is missing")
	}
	percentage, err := decimal.NewFromString(rate)
	if err != nil {
		return fmt.Errorf("parse sales commission rate: %w", err)
	}
	revenue := decimal.NewFromFloat(cmd.BalanceCost).Round(service.UsageBillingMonetaryScale)
	cost := decimal.NewFromFloat(*cmd.SalesCost).Round(service.UsageBillingMonetaryScale)
	profit := revenue.Sub(cost)
	commission := profit.Mul(percentage).Div(decimal.NewFromInt(100)).Round(service.UsageBillingMonetaryScale)
	_, err = tx.ExecContext(ctx, `
INSERT INTO sales_commission_events
 (request_id, api_key_id, customer_user_id, partner_id, rule_id, revenue, cost, profit,
  commission_rate, commission, currency, model, occurred_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'USD',$11,$12)
ON CONFLICT (request_id, api_key_id) DO NOTHING`,
		cmd.RequestID, cmd.APIKeyID, cmd.UserID, partnerID, ruleID,
		revenue.StringFixed(8), cost.StringFixed(8), profit.StringFixed(8),
		percentage.StringFixed(8), commission.StringFixed(8), cmd.Model, at)
	if err != nil {
		return fmt.Errorf("append sales billing event: %w", err)
	}
	return nil
}
