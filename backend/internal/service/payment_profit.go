package service

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type ProfitMetrics struct {
	RevenueUSD                float64            `json:"revenue_usd"`
	CostUSD                   float64            `json:"cost_usd"`
	ProfitUSD                 float64            `json:"profit_usd"`
	NominalUsageUSD           float64            `json:"nominal_usage_usd"`
	PaymentReceivedByCurrency map[string]float64 `json:"payment_received_by_currency,omitempty"`
}

type ProfitDailyStats struct {
	Date                        string  `json:"date"`
	BalanceRevenueUSD           float64 `json:"balance_revenue_usd"`
	BalanceCostUSD              float64 `json:"balance_cost_usd"`
	BalanceProfitUSD            float64 `json:"balance_profit_usd"`
	SubscriptionRevenueUSD      float64 `json:"subscription_revenue_usd"`
	SubscriptionCostUSD         float64 `json:"subscription_cost_usd"`
	SubscriptionProfitUSD       float64 `json:"subscription_profit_usd"`
	SubscriptionNominalUsageUSD float64 `json:"subscription_nominal_usage_usd"`
	TotalRevenueUSD             float64 `json:"total_revenue_usd"`
	TotalCostUSD                float64 `json:"total_cost_usd"`
	TotalProfitUSD              float64 `json:"total_profit_usd"`
}

type ProfitReport struct {
	StartDate   string              `json:"start_date"`
	EndDate     string              `json:"end_date"`
	Summary     ProfitReportSummary `json:"summary"`
	DailySeries []ProfitDailyStats  `json:"daily_series"`
}

type ProfitReportSummary struct {
	Balance      ProfitMetrics `json:"balance"`
	Subscription ProfitMetrics `json:"subscription"`
	Total        ProfitMetrics `json:"total"`
}

type profitDailyAccumulator struct {
	balanceRevenue      float64
	balanceCost         float64
	subscriptionRevenue float64
	subscriptionCost    float64
	subscriptionUsage   float64
}

type profitRevenueEvent struct {
	start        time.Time
	end          time.Time
	orderAmount  float64
	paidAmount   float64
	currency     string
	status       string
	refundAmount sql.NullFloat64
}

// GetProfitReport separates balance-billed revenue from subscription revenue.
// Subscription revenue is recognized evenly over its recorded service period;
// request costs are recognized on the request date using the upstream account cost.
func (s *PaymentService) GetProfitReport(ctx context.Context, start, end time.Time) (*ProfitReport, error) {
	return s.getProfitReport(ctx, start, end, false)
}

// GetTodayProfitReport returns one day of profit without the separate cash receipts breakdown.
func (s *PaymentService) GetTodayProfitReport(ctx context.Context, today time.Time) (*ProfitReport, error) {
	return s.getProfitReport(ctx, today, today.AddDate(0, 0, 1), true)
}

func (s *PaymentService) getProfitReport(ctx context.Context, start, end time.Time, todayOnly bool) (*ProfitReport, error) {
	if s.entClient == nil {
		return nil, fmt.Errorf("payment database is unavailable")
	}
	loc := timezone.Location()
	start = timezone.StartOfDay(start.In(loc))
	end = timezone.StartOfDay(end.In(loc))
	if !end.After(start) {
		return nil, fmt.Errorf("profit report range is invalid")
	}
	if end.After(start.AddDate(0, 0, 366)) {
		return nil, fmt.Errorf("profit report range cannot exceed 366 days")
	}
	dayCount := 0
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		dayCount++
	}
	accumulators := make(map[string]*profitDailyAccumulator, dayCount)
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		accumulators[day.Format("2006-01-02")] = &profitDailyAccumulator{}
	}
	if err := s.loadProfitCosts(ctx, start, end, accumulators); err != nil {
		return nil, err
	}
	revenueAllocationStart := start
	if todayOnly {
		revenueAllocationStart = timezone.StartOfMonth(start)
	}
	if err := s.loadProfitSubscriptionRevenue(ctx, start, end, revenueAllocationStart, accumulators); err != nil {
		return nil, err
	}
	if todayOnly {
		return buildProfitReport(start, end, accumulators, nil), nil
	}
	receivedByCurrency, err := s.loadProfitPaymentReceived(ctx, start, end)
	if err != nil {
		return nil, err
	}
	return buildProfitReport(start, end, accumulators, receivedByCurrency), nil
}

func (s *PaymentService) loadProfitCosts(ctx context.Context, start, end time.Time, daily map[string]*profitDailyAccumulator) error {
	rows, err := s.entClient.QueryContext(ctx, `
		SELECT TO_CHAR(created_at AT TIME ZONE $3, 'YYYY-MM-DD') AS report_date,
			COALESCE(SUM(actual_cost) FILTER (WHERE billing_type = 0), 0) AS balance_revenue,
			COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)) FILTER (WHERE billing_type = 0), 0) AS balance_cost,
			COALESCE(SUM(actual_cost) FILTER (WHERE billing_type = 1), 0) AS subscription_usage,
			COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)) FILTER (WHERE billing_type = 1), 0) AS subscription_cost
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2
			AND (actual_cost > 0 OR COALESCE(account_stats_cost, total_cost) > 0)
		GROUP BY report_date
	`, start, end, timezone.Name())
	if err != nil {
		return fmt.Errorf("query profit usage costs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var date string
		var balanceRevenue, balanceCost, subscriptionUsage, subscriptionCost float64
		if err := rows.Scan(&date, &balanceRevenue, &balanceCost, &subscriptionUsage, &subscriptionCost); err != nil {
			return fmt.Errorf("scan profit usage costs: %w", err)
		}
		if day := daily[date]; day != nil {
			day.balanceRevenue = balanceRevenue
			day.balanceCost = balanceCost
			day.subscriptionUsage = subscriptionUsage
			day.subscriptionCost = subscriptionCost
		}
	}
	return rows.Err()
}

func (s *PaymentService) loadProfitSubscriptionRevenue(ctx context.Context, start, end, revenueAllocationStart time.Time, daily map[string]*profitDailyAccumulator) error {
	rows, err := s.entClient.QueryContext(ctx, `
		SELECT e.service_start_at, e.service_end_at, e.order_amount,
			po.status, po.refund_amount
		FROM subscription_revenue_events e
		JOIN payment_orders po ON po.id = e.order_id
		WHERE e.service_start_at < $1 AND e.service_end_at > $2
	`, end, start)
	if err != nil {
		return fmt.Errorf("query subscription revenue events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var event profitRevenueEvent
		if err := rows.Scan(&event.start, &event.end, &event.orderAmount, &event.status, &event.refundAmount); err != nil {
			return fmt.Errorf("scan subscription revenue event: %w", err)
		}
		refundAmount := confirmedSubscriptionRefund(event.status, event.refundAmount)
		netRevenue := math.Max(0, event.orderAmount-refundAmount)
		allocationStart := start
		// Include earlier service days only when they affect the final day's rounding in the monthly report.
		if !event.start.Before(revenueAllocationStart) && !event.end.After(end) {
			allocationStart = revenueAllocationStart
		}
		for date, amount := range splitProfitAmountByDay(netRevenue, event.start, event.end, allocationStart, end, timezone.Location()) {
			if day := daily[date]; day != nil {
				day.subscriptionRevenue += amount
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func (s *PaymentService) loadProfitPaymentReceived(ctx context.Context, start, end time.Time) (map[string]float64, error) {
	rows, err := s.entClient.QueryContext(ctx, `
		SELECT e.order_amount, e.paid_amount, e.payment_currency, po.status, po.refund_amount
		FROM subscription_revenue_events e
		JOIN payment_orders po ON po.id = e.order_id
		WHERE po.paid_at >= $1 AND po.paid_at < $2
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("query subscription payment receipts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	received := make(map[string]float64)
	for rows.Next() {
		var event profitRevenueEvent
		if err := rows.Scan(&event.orderAmount, &event.paidAmount, &event.currency, &event.status, &event.refundAmount); err != nil {
			return nil, fmt.Errorf("scan subscription payment receipt: %w", err)
		}
		if event.orderAmount <= 0 || event.paidAmount <= 0 || event.currency == "" {
			continue
		}
		netRevenue := math.Max(0, event.orderAmount-confirmedSubscriptionRefund(event.status, event.refundAmount))
		received[event.currency] += event.paidAmount * (netRevenue / event.orderAmount)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return received, nil
}

func confirmedSubscriptionRefund(status string, refund sql.NullFloat64) float64 {
	if !refund.Valid || (status != OrderStatusRefunded && status != OrderStatusPartiallyRefunded) {
		return 0
	}
	return math.Max(0, refund.Float64)
}

func splitProfitAmountByDay(amount float64, eventStart, eventEnd, rangeStart, rangeEnd time.Time, loc *time.Location) map[string]float64 {
	allocations := make(map[string]float64)
	if amount <= 0 || !eventEnd.After(eventStart) {
		return allocations
	}
	periodStart := eventStart.In(loc)
	periodEnd := eventEnd.In(loc)
	firstDay := timezone.StartOfDay(profitMaxTime(periodStart, rangeStart.In(loc)))
	lastMoment := profitMinTime(periodEnd, rangeEnd.In(loc))
	if !lastMoment.After(profitMaxTime(periodStart, rangeStart.In(loc))) {
		return allocations
	}
	periodSeconds := eventEnd.Sub(eventStart).Seconds()
	coveredWholePeriod := !rangeStart.After(eventStart) && !rangeEnd.Before(eventEnd)
	allocated := 0.0
	for dayStart := firstDay; dayStart.Before(lastMoment); dayStart = dayStart.AddDate(0, 0, 1) {
		dayEnd := dayStart.AddDate(0, 0, 1)
		overlapStart := profitMaxTime(periodStart, dayStart)
		overlapEnd := profitMinTime(periodEnd, profitMinTime(dayEnd, rangeEnd.In(loc)))
		if !overlapEnd.After(overlapStart) {
			continue
		}
		portion := amount * overlapEnd.Sub(overlapStart).Seconds() / periodSeconds
		if coveredWholePeriod && !dayEnd.Before(periodEnd) {
			portion = amount - allocated
		}
		portion = roundProfitValue(portion)
		allocations[dayStart.Format("2006-01-02")] += portion
		allocated += portion
	}
	return allocations
}

func buildProfitReport(start, end time.Time, daily map[string]*profitDailyAccumulator, receivedByCurrency map[string]float64) *ProfitReport {
	report := &ProfitReport{
		StartDate: start.Format("2006-01-02"),
		EndDate:   end.AddDate(0, 0, -1).Format("2006-01-02"),
		Summary: ProfitReportSummary{
			Balance:      ProfitMetrics{},
			Subscription: ProfitMetrics{PaymentReceivedByCurrency: make(map[string]float64)},
			Total:        ProfitMetrics{},
		},
		DailySeries: make([]ProfitDailyStats, 0, len(daily)),
	}
	for currency, amount := range receivedByCurrency {
		report.Summary.Subscription.PaymentReceivedByCurrency[currency] = roundProfitValue(amount)
	}
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		values := daily[date]
		if values == nil {
			values = &profitDailyAccumulator{}
		}
		balanceProfit := values.balanceRevenue - values.balanceCost
		subscriptionProfit := values.subscriptionRevenue - values.subscriptionCost
		totalRevenue := values.balanceRevenue + values.subscriptionRevenue
		totalCost := values.balanceCost + values.subscriptionCost
		stats := ProfitDailyStats{
			Date:                        date,
			BalanceRevenueUSD:           roundProfitValue(values.balanceRevenue),
			BalanceCostUSD:              roundProfitValue(values.balanceCost),
			BalanceProfitUSD:            roundProfitValue(balanceProfit),
			SubscriptionRevenueUSD:      roundProfitValue(values.subscriptionRevenue),
			SubscriptionCostUSD:         roundProfitValue(values.subscriptionCost),
			SubscriptionProfitUSD:       roundProfitValue(subscriptionProfit),
			SubscriptionNominalUsageUSD: roundProfitValue(values.subscriptionUsage),
			TotalRevenueUSD:             roundProfitValue(totalRevenue),
			TotalCostUSD:                roundProfitValue(totalCost),
			TotalProfitUSD:              roundProfitValue(totalRevenue - totalCost),
		}
		report.DailySeries = append(report.DailySeries, stats)
		report.Summary.Balance.RevenueUSD += values.balanceRevenue
		report.Summary.Balance.CostUSD += values.balanceCost
		report.Summary.Balance.NominalUsageUSD += values.balanceRevenue
		report.Summary.Subscription.RevenueUSD += values.subscriptionRevenue
		report.Summary.Subscription.CostUSD += values.subscriptionCost
		report.Summary.Subscription.NominalUsageUSD += values.subscriptionUsage
	}
	report.Summary.Balance.ProfitUSD = report.Summary.Balance.RevenueUSD - report.Summary.Balance.CostUSD
	report.Summary.Subscription.ProfitUSD = report.Summary.Subscription.RevenueUSD - report.Summary.Subscription.CostUSD
	report.Summary.Total.RevenueUSD = report.Summary.Balance.RevenueUSD + report.Summary.Subscription.RevenueUSD
	report.Summary.Total.CostUSD = report.Summary.Balance.CostUSD + report.Summary.Subscription.CostUSD
	report.Summary.Total.ProfitUSD = report.Summary.Total.RevenueUSD - report.Summary.Total.CostUSD
	report.Summary.Total.NominalUsageUSD = report.Summary.Balance.NominalUsageUSD + report.Summary.Subscription.NominalUsageUSD
	roundProfitMetrics(&report.Summary.Balance)
	roundProfitMetrics(&report.Summary.Subscription)
	roundProfitMetrics(&report.Summary.Total)
	return report
}

func roundProfitMetrics(metrics *ProfitMetrics) {
	metrics.RevenueUSD = roundProfitValue(metrics.RevenueUSD)
	metrics.CostUSD = roundProfitValue(metrics.CostUSD)
	metrics.ProfitUSD = roundProfitValue(metrics.ProfitUSD)
	metrics.NominalUsageUSD = roundProfitValue(metrics.NominalUsageUSD)
}

func roundProfitValue(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}

func profitMaxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func profitMinTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
