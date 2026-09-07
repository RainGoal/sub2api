package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSalesFiltersKeepOwnershipParameterized(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	where, args := salesWhere(service.SalesFilter{PartnerID: 42, StartAt: &start, EndAt: &end}, "l", "l.occurred_at")
	require.Equal(t, " WHERE TRUE AND l.partner_id=$1 AND l.occurred_at >= $2 AND l.occurred_at < $3", where)
	require.Equal(t, []any{int64(42), start, end}, args)
	query, args := salesPage("SELECT id FROM ledger"+where, args, service.SalesFilter{Page: 3, PageSize: 20})
	require.Contains(t, query, "LIMIT $4 OFFSET $5")
	require.Equal(t, []any{int64(42), start, end, 20, 40}, args)
}
