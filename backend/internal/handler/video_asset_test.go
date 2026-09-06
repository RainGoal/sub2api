package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type videoAssetHandlerStub struct {
	enabled      bool
	uploaded     []byte
	userID       int64
	maxPerMinute int
	dailyLimit   int64
}

func (s *videoAssetHandlerStub) Config(context.Context) (*service.VideoAssetConfig, error) {
	return &service.VideoAssetConfig{Enabled: s.enabled, MaxUploadsPerMinute: s.maxPerMinute, DailyLimitBytes: s.dailyLimit, Limits: map[string]service.VideoAssetLimit{
		"image": {MaxBytes: 16, MIMETypes: []string{"image/png"}},
		"video": {MaxBytes: service.VideoAssetMaxBytes, MIMETypes: []string{"video/mp4"}},
	}}, nil
}

func (s *videoAssetHandlerStub) Upload(_ context.Context, userID int64, media string, file io.ReadSeeker, size int64) (*service.VideoAsset, error) {
	s.uploaded, _ = io.ReadAll(file)
	s.userID = userID
	return &service.VideoAsset{URL: "https://storage.example.com/file", MediaType: media, Size: size}, nil
}

func videoAssetHandlerFixture(t *testing.T, authenticated bool) (*gin.Engine, *VideoAssetHandler, *videoAssetHandlerStub, *redis.Client) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	assets := &videoAssetHandlerStub{enabled: true, maxPerMinute: service.DefaultVideoUploadMaxPerMinute, dailyLimit: service.DefaultVideoUploadDailyLimitMiB << 20}
	h := NewVideoAssetHandler(nil, client)
	h.service = assets
	router := gin.New()
	if authenticated {
		router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42}) })
	}
	router.GET("/api/v1/video-assets/config", h.Config)
	router.POST("/api/v1/video-assets", h.Upload)
	return router, h, assets, client
}

func videoAssetMultipart(t *testing.T, media string, files ...string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("media_type", media))
	for _, value := range files {
		part, err := writer.CreateFormFile("file", "../../untrusted.png")
		require.NoError(t, err)
		_, err = io.WriteString(part, value)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-assets", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestVideoAssetHandlerRequiresLogin(t *testing.T) {
	router, _, stub, _ := videoAssetHandlerFixture(t, false)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/video-assets/config", nil),
		videoAssetMultipart(t, "image", "image data"),
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code)
	}
	require.Empty(t, stub.uploaded)
}

func TestVideoAssetHandlerUploadAndConfigEnvelope(t *testing.T) {
	router, _, stub, _ := videoAssetHandlerFixture(t, true)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, videoAssetMultipart(t, "image", "image data"))
	require.Equal(t, 201, w.Code, w.Body.String())
	var result struct {
		Code int                `json:"code"`
		Data service.VideoAsset `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Zero(t, result.Code)
	require.Equal(t, int64(10), result.Data.Size)
	require.Equal(t, int64(42), stub.userID)
	require.Equal(t, "image data", string(stub.uploaded))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/video-assets/config", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"enabled":true`)
	require.NotContains(t, w.Body.String(), "secret")
}

func TestVideoAssetHandlerValidatesMultipartAndDisabledState(t *testing.T) {
	for _, tc := range []struct {
		name, media string
		files       []string
		disabled    bool
		code        int
	}{
		{"missing", "image", nil, false, 400},
		{"multiple", "image", []string{"one", "two"}, false, 400},
		{"type", "document", []string{"one"}, false, 400},
		{"size", "image", []string{strings.Repeat("x", 17)}, false, 413},
		{"disabled", "image", []string{"one"}, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _, stub, _ := videoAssetHandlerFixture(t, true)
			stub.enabled = !tc.disabled
			w := httptest.NewRecorder()
			router.ServeHTTP(w, videoAssetMultipart(t, tc.media, tc.files...))
			require.Equal(t, tc.code, w.Code, w.Body.String())
			require.Empty(t, stub.uploaded)
		})
	}
}

func TestVideoAssetHandlerRateAndByteBudgets(t *testing.T) {
	router, _, _, client := videoAssetHandlerFixture(t, true)
	ctx := context.Background()
	key := "video-assets:bytes:" + time.Now().UTC().Format("2006-01-02") + ":42"
	require.NoError(t, client.Set(ctx, key, (1<<30)-4, time.Hour).Err())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, videoAssetMultipart(t, "image", "12345"))
	require.Equal(t, 429, w.Code)
	require.Contains(t, w.Body.String(), "UPLOAD_QUOTA_EXCEEDED")
	require.NoError(t, client.Set(ctx, "rate_limit:video-assets:user:42", 20, time.Minute).Err())
	w = httptest.NewRecorder()
	router.ServeHTTP(w, videoAssetMultipart(t, "image", "x"))
	require.Equal(t, 429, w.Code)
	require.Contains(t, w.Body.String(), "UPLOAD_RATE_LIMITED")
	require.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestVideoAssetHandlerRateChangesPreserveCurrentWindow(t *testing.T) {
	router, _, stub, client := videoAssetHandlerFixture(t, true)
	for i, tc := range []struct{ limit, status int }{
		{1, 201}, {1, 429}, {3, 201}, {2, 429},
	} {
		stub.maxPerMinute = tc.limit
		w := httptest.NewRecorder()
		router.ServeHTTP(w, videoAssetMultipart(t, "image", "x"))
		require.Equal(t, tc.status, w.Code, w.Body.String())
		if tc.status == 429 {
			require.Contains(t, w.Body.String(), "UPLOAD_RATE_LIMITED")
		}
		count, err := client.Get(context.Background(), "rate_limit:video-assets:user:42").Int()
		require.NoError(t, err)
		require.Equal(t, i+1, count, "saving a new rate must not reset the request count")
	}
}

func TestVideoAssetHandlerDailyLimitChangesPreserveUsage(t *testing.T) {
	router, _, stub, client := videoAssetHandlerFixture(t, true)
	ctx := context.Background()
	key := "video-assets:bytes:" + time.Now().UTC().Format("2006-01-02") + ":42"
	used := int64(1 << 20)
	require.NoError(t, client.Set(ctx, key, used, time.Hour).Err())
	for _, tc := range []struct {
		limit  int64
		status int
	}{
		{1 << 20, 429}, {2 << 20, 201}, {1 << 20, 429},
	} {
		stub.dailyLimit = tc.limit
		w := httptest.NewRecorder()
		router.ServeHTTP(w, videoAssetMultipart(t, "image", "123"))
		require.Equal(t, tc.status, w.Code, w.Body.String())
		if tc.status == 201 {
			used += 3
		} else {
			require.Contains(t, w.Body.String(), "UPLOAD_QUOTA_EXCEEDED")
		}
		count, err := client.Get(ctx, key).Int64()
		require.NoError(t, err)
		require.Equal(t, used, count, "quota changes must retain previously received bytes")
	}
}

func TestVideoAssetHandlerConcurrentSlotsAreReleased(t *testing.T) {
	router, h, _, _ := videoAssetHandlerFixture(t, true)
	require.True(t, h.acquire(42))
	require.True(t, h.acquire(42))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, videoAssetMultipart(t, "image", "x"))
	require.Equal(t, 429, w.Code)
	require.Contains(t, w.Body.String(), "UPLOAD_BUSY")
	h.release(42)
	h.release(42)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, videoAssetMultipart(t, "image", "x"))
	require.Equal(t, 201, w.Code)
	require.Empty(t, h.active)
	require.Empty(t, h.slots)
}

func TestVideoAssetHandlerCapsRequestBody(t *testing.T) {
	router, _, stub, _ := videoAssetHandlerFixture(t, true)
	const boundary = "upload-boundary"
	start := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"file.mp4\"\r\n\r\n"
	body := io.MultiReader(strings.NewReader(start), io.LimitReader(videoAssetZeroReader{}, service.VideoAssetMaxBytes+(2<<20)))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-assets", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Content-Length", strconv.FormatInt(service.VideoAssetMaxBytes+(2<<20), 10))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 413, w.Code)
	require.Empty(t, stub.uploaded)
}

type videoAssetZeroReader struct{}

func (videoAssetZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
