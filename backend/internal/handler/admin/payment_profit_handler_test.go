package admin

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestParseProfitReportRange(t *testing.T) {
	loc := timezone.Location()
	start, end, err := parseProfitReportRange("2026-11-01", "2026-11-01")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), start)
	require.Equal(t, start.AddDate(0, 0, 1), end)

	_, _, err = parseProfitReportRange("2026-02-30", "2026-03-01")
	require.ErrorIs(t, err, errProfitReportDateRangeInvalid)
	_, _, err = parseProfitReportRange("2026-11-02", "2026-11-01")
	require.ErrorIs(t, err, errProfitReportDateRangeInvalid)
	_, _, err = parseProfitReportRange("2025-01-01", "2026-01-02")
	require.ErrorIs(t, err, errProfitReportDateRangeTooLarge)
}
