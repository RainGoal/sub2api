package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGetAccountWindowStatsBatchUsesAccountCostAndFillsEmptyAccounts(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`(?s)SUM\(COALESCE\(account_stats_cost, total_cost\) \* COALESCE\(account_rate_multiplier, 1\)\).*WHERE account_id = ANY\(\$1\) AND created_at >= \$2`).
		WithArgs("{1,2,3}", start).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "requests", "tokens", "cost", "standard_cost", "user_cost"}).
			AddRow(int64(1), int64(2), int64(100), 1.25, 1.0, 1.5).
			AddRow(int64(2), int64(1), int64(50), 0.75, 0.5, 1.0))

	stats, err := repo.GetAccountWindowStatsBatch(context.Background(), []int64{1, 2, 3}, start)
	require.NoError(t, err)
	require.Len(t, stats, 3)
	require.Equal(t, 1.25, stats[1].Cost)
	require.Equal(t, 0.75, stats[2].Cost)
	require.Equal(t, 0.0, stats[3].Cost)
	require.NoError(t, mock.ExpectationsWereMet())
}
