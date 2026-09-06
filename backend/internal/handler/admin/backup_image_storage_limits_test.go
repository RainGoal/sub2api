package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageStorageLimitsRepo struct {
	service.SettingRepository
	value string
}

func (r *imageStorageLimitsRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}

func (r *imageStorageLimitsRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

func TestImageStorageLimitsAdminContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &imageStorageLimitsRepo{}
	settings := service.NewImageStorageSettingService(repo, nil, nil, nil, config.ImageStorageConfig{})
	h := NewBackupHandler(nil, nil, settings)
	router := gin.New()
	const path = "/api/v1/admin/backups/image-storage"
	router.PUT(path, h.UpdateImageStorageConfig)
	router.GET(path, h.GetImageStorageConfig)
	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	w := send(`{"video_upload_max_per_minute":40,"video_upload_daily_limit_mib":2048}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, 200, w.Code)
	var got struct {
		Data struct {
			Config service.ImageStorageSettings `json:"config"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, 40, got.Data.Config.VideoUploadMaxPerMinute)
	require.Equal(t, int64(2048), got.Data.Config.VideoUploadDailyLimitMiB)
	valid := repo.value
	for _, body := range []string{
		`{"video_upload_max_per_minute":-1}`,
		`{"video_upload_max_per_minute":1.5}`,
		`{"video_upload_daily_limit_mib":1.5}`,
		`{"video_upload_daily_limit_mib":"2048"}`,
		`{"video_upload_daily_limit_mib":1048577}`,
	} {
		w := send(body)
		require.Equal(t, 400, w.Code, w.Body.String())
		require.Equal(t, valid, repo.value, "invalid input must not replace the saved limits")
	}
}
