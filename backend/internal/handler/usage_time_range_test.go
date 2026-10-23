package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUserUsagePreciseTimeRange(t *testing.T) {
	for _, path := range []string{"/usage", "/usage/stats"} {
		repo := &userUsageRepoCapture{}
		router := newUserUsageRequestTypeTestRouter(repo)
		query := url.Values{"start_date": {"2026-10-21T23:59:12+08:00"}, "end_date": {"2026-10-22T23:59:12+08:00"}, "timezone": {"Asia/Shanghai"}}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query.Encode(), nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		filters := repo.listFilters
		if path == "/usage/stats" {
			filters = repo.statsFilters
		}
		require.NotNil(t, filters.StartTime)
		require.NotNil(t, filters.EndTime)
		require.Equal(t, "2026-10-21T15:59:12Z", filters.StartTime.UTC().Format(time.RFC3339))
		require.Equal(t, 24*time.Hour, filters.EndTime.Sub(*filters.StartTime))
	}
}
