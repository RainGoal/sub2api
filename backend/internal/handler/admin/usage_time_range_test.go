package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminUsageTimeRange(t *testing.T) {
	for _, path := range []string{"/admin/usage", "/admin/usage/stats"} {
		for _, tc := range []struct{ name, start, end, wantStart, wantEnd string }{
			{"legacy today", "2026-10-22", "2026-10-22", "2026-10-21T16:00:00Z", "2026-10-22T16:00:00Z"},
			{"last 24h at 2359", "2026-10-21T23:59:12+08:00", "2026-10-22T23:59:12+08:00", "2026-10-21T15:59:12Z", "2026-10-22T15:59:12Z"},
			{"seconds", "2026-10-22T12:30:01Z", "2026-10-22T12:30:09Z", "2026-10-22T12:30:01Z", "2026-10-22T12:30:09Z"},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				repo := &adminUsageRepoCapture{}
				router := newAdminUsageRequestTypeTestRouter(repo)
				query := url.Values{"start_date": {tc.start}, "end_date": {tc.end}, "timezone": {"Asia/Shanghai"}, "nocache": {"1"}}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query.Encode(), nil))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				filters := repo.listFilters
				if path == "/admin/usage/stats" {
					filters = repo.statsFilters
				}
				require.NotNil(t, filters.StartTime)
				require.NotNil(t, filters.EndTime)
				require.Equal(t, tc.wantStart, filters.StartTime.UTC().Format(time.RFC3339))
				require.Equal(t, tc.wantEnd, filters.EndTime.UTC().Format(time.RFC3339))
			})
		}
		for _, dates := range [][2]string{{"bad", "2026-10-22"}, {"2026-10-22T12:00:00Z", "2026-10-22T12:00:00Z"}, {"2026-10-23T00:00:00Z", "2026-10-22T00:00:00Z"}} {
			repo := &adminUsageRepoCapture{}
			router := newAdminUsageRequestTypeTestRouter(repo)
			rec := httptest.NewRecorder()
			query := url.Values{"start_date": {dates[0]}, "end_date": {dates[1]}, "nocache": {"1"}}
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query.Encode(), nil))
			require.Equal(t, http.StatusBadRequest, rec.Code)
		}
	}
}

func TestDashboardPreciseTimeRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	query := url.Values{"start_date": {"2026-10-22T12:30:01+08:00"}, "end_date": {"2026-10-22T12:30:09+08:00"}, "timezone": {"Asia/Shanghai"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
	start, end := parseTimeRange(c)
	require.Equal(t, "2026-10-22T04:30:01Z", start.UTC().Format(time.RFC3339))
	require.Equal(t, 8*time.Second, end.Sub(start))
}
