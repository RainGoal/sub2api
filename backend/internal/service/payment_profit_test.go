package service

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSplitProfitAmountByDayRecognizesRevenueAcrossServiceDays(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 3)

	got := splitProfitAmountByDay(100, start, end, start, end, loc)
	require.Equal(t, map[string]float64{
		"2026-11-01": 33.333333,
		"2026-11-02": 33.333333,
		"2026-11-03": 33.333334,
	}, got)

	partial := splitProfitAmountByDay(100, start, end, start.AddDate(0, 0, 1), end, loc)
	require.Equal(t, map[string]float64{
		"2026-11-02": 33.333333,
		"2026-11-03": 33.333333,
	}, partial)
}

func TestConfirmedSubscriptionRefundOnlyCountsFinalRefunds(t *testing.T) {
	refund := sql.NullFloat64{Float64: 20, Valid: true}
	require.Zero(t, confirmedSubscriptionRefund(OrderStatusRefundPending, refund))
	require.Zero(t, confirmedSubscriptionRefund(OrderStatusRefundFailed, refund))
	require.Equal(t, 20.0, confirmedSubscriptionRefund(OrderStatusPartiallyRefunded, refund))
	require.Equal(t, 20.0, confirmedSubscriptionRefund(OrderStatusRefunded, refund))
}

func TestBuildProfitReportKeepsBalanceAndSubscriptionMetricsSeparate(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	daily := map[string]*profitDailyAccumulator{
		"2026-11-01": {
			balanceRevenue:      10,
			balanceCost:         4,
			subscriptionRevenue: 5,
			subscriptionCost:    3,
			subscriptionUsage:   7.5,
		},
	}

	report := buildProfitReport(start, end, daily, map[string]float64{"CNY": 35.5})
	require.Equal(t, 6.0, report.Summary.Balance.ProfitUSD)
	require.Equal(t, 2.0, report.Summary.Subscription.ProfitUSD)
	require.Equal(t, 8.0, report.Summary.Total.ProfitUSD)
	require.Equal(t, 7.5, report.Summary.Subscription.NominalUsageUSD)
	require.Equal(t, 35.5, report.Summary.Subscription.PaymentReceivedByCurrency["CNY"])
	require.Equal(t, 1, len(report.DailySeries))
}
