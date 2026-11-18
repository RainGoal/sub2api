package service

import (
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestTodayProfitReportMatchesMonthlyBreakdown(t *testing.T) {
	loc := timezone.Location()
	today := time.Date(2026, time.February, 3, 0, 0, 0, 0, loc)
	monthStart := timezone.StartOfMonth(today)
	tomorrow := today.AddDate(0, 0, 1)
	date := today.Format("2006-01-02")
	for _, tc := range []struct {
		name                 string
		serviceStart, end    time.Time
		wantRecognizedAmount float64
	}{
		{"final day of an in-month subscription", monthStart, tomorrow, 33.333334},
		{"final day of a cross-month subscription", monthStart.AddDate(0, 0, -1), today.Add(12 * time.Hour), 14.285714},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			svc := &PaymentService{entClient: dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))}

			expectProfitQueries := func(start time.Time) {
				mock.ExpectQuery(`FROM usage_logs WHERE created_at >= \$1 AND created_at < \$2`).
					WithArgs(start, tomorrow, timezone.Name()).
					WillReturnRows(sqlmock.NewRows([]string{"report_date", "balance_revenue", "balance_cost", "subscription_usage", "subscription_cost"}).
						AddRow(date, 10, 4, 15, 5))
				mock.ExpectQuery(`WHERE e.service_start_at < \$1 AND e.service_end_at > \$2`).
					WithArgs(tomorrow, start).
					WillReturnRows(sqlmock.NewRows([]string{"service_start_at", "service_end_at", "order_amount", "status", "refund_amount"}).
						AddRow(tc.serviceStart, tc.end, 120, OrderStatusPartiallyRefunded, 20))
			}

			expectProfitQueries(today)
			todayReport, err := svc.GetTodayProfitReport(t.Context(), today)
			require.NoError(t, err)
			require.Equal(t, date, todayReport.StartDate)
			require.Equal(t, date, todayReport.EndDate)
			require.Len(t, todayReport.DailySeries, 1)
			require.Equal(t, tc.wantRecognizedAmount, todayReport.DailySeries[0].SubscriptionRevenueUSD)
			require.Empty(t, todayReport.Summary.Subscription.PaymentReceivedByCurrency)

			expectProfitQueries(monthStart)
			mock.ExpectQuery(`WHERE po.paid_at >= \$1 AND po.paid_at < \$2`).
				WithArgs(monthStart, tomorrow).
				WillReturnRows(sqlmock.NewRows([]string{"order_amount", "paid_amount", "payment_currency", "status", "refund_amount"}).
					AddRow(120, 840, "CNY", OrderStatusPartiallyRefunded, 20))
			monthReport, err := svc.GetProfitReport(t.Context(), monthStart, tomorrow)
			require.NoError(t, err)
			require.Equal(t, todayReport.DailySeries[0], monthReport.DailySeries[2])
			require.Equal(t, map[string]float64{"CNY": 700}, monthReport.Summary.Subscription.PaymentReceivedByCurrency)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

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
