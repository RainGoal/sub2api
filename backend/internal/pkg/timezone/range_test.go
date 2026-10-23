package timezone

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseRangeBound(t *testing.T) {
	for _, tc := range []struct {
		name, value, zone, want string
		end                     bool
	}{
		{"date start", "2026-10-22", "Asia/Shanghai", "2026-10-21T16:00:00Z", false},
		{"inclusive date end", "2026-10-22", "Asia/Shanghai", "2026-10-22T16:00:00Z", true},
		{"precise start", "2026-10-21T23:59:12+08:00", "Asia/Shanghai", "2026-10-21T15:59:12Z", false},
		{"precise end", "2026-10-22T23:59:12+08:00", "Asia/Shanghai", "2026-10-22T15:59:12Z", true},
		{"UTC with fractions", "2026-10-22T15:59:12.123Z", "Asia/Shanghai", "2026-10-22T15:59:12.123Z", true},
		{"DST calendar end", "2026-03-08", "America/New_York", "2026-03-09T04:00:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRangeBound(tc.value, tc.zone, tc.end)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.UTC().Format(time.RFC3339Nano))
		})
	}
	for _, value := range []string{"", "2026-02-30", "2026-10-22T24:00:00Z", "2026-10-22T12:30:00", "invalid"} {
		_, err := ParseRangeBound(value, "Asia/Shanghai", false)
		require.Error(t, err, value)
	}
}
