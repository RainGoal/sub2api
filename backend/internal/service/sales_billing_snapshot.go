package service

import (
	"math"
	"time"
)

// captureSalesBillingCost shares the account-statistics cost convention without
// changing user pricing, quota accounting, or the existing request fingerprint.
func captureSalesBillingCost(cmd *UsageBillingCommand, usage *UsageLog, p *postUsageBillingParams) {
	if cmd == nil || p == nil || p.Cost == nil || p.IsSubscriptionBill {
		return
	}
	cmd.SalesConsumedAt = time.Now().UTC()
	base, multiplier := p.Cost.TotalCost, p.AccountRateMultiplier
	if usage != nil {
		if !usage.CreatedAt.IsZero() {
			cmd.SalesConsumedAt = usage.CreatedAt
		}
		if usage.AccountStatsCost != nil {
			base = *usage.AccountStatsCost
		}
		if usage.AccountRateMultiplier != nil {
			multiplier = *usage.AccountRateMultiplier
		}
	}
	if !validSalesCost(base) || !validSalesCost(multiplier) {
		return // Leave invalid costs unset: attributed billing must reject the snapshot, never assume zero cost.
	}
	cost := base * multiplier
	if !validSalesCost(cost) {
		return
	}
	cost = QuantizeUsageBillingAmount(cost)
	cmd.SalesCost = &cost
}

func validSalesCost(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
