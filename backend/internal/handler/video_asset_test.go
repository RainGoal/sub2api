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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type videoAssetHandlerStub struct {
	enabled      bool
	uploaded     []byte
	userID       int64
	maxPerMinute int
	dailyLimit   int64
}

type videoAssetTestLimiter struct{}

func (videoAssetTestLimiter) Allow(context.Context, int64, int) (bool, time.Duration, error) {
	return true, 0, nil
}

func (videoAssetTestLimiter) ReserveBytes(context.Context, int64, int64, int64) (bool, error) {
	return true, nil
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

func videoAssetHandlerFixture(t *testing.T, authenticated bool) (*gin.Engine, *VideoAssetHandler, *videoAssetHandlerStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	assets := &videoAssetHandlerStub{enabled: true, maxPerMinute: service.DefaultVideoUploadMaxPerMinute, dailyLimit: service.DefaultVideoUploadDailyLimitMiB << 20}
	h := NewVideoAssetHandler(nil, videoAssetTestLimiter{})
	h.service = assets
	router := gin.New()
	if authenticated {
		router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42}) })
	}
	router.GET("/api/v1/video-assets/config", h.Config)
	router.POST("/api/v1/video-assets", h.Upload)
	return router, h, assets
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
	router, _, stub := videoAssetHandlerFixture(t, false)
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
	router, _, stub := videoAssetHandlerFixture(t, true)
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
			router, _, stub := videoAssetHandlerFixture(t, true)
			stub.enabled = !tc.disabled
			w := httptest.NewRecorder()
			router.ServeHTTP(w, videoAssetMultipart(t, tc.media, tc.files...))
			require.Equal(t, tc.code, w.Code, w.Body.String())
			require.Empty(t, stub.uploaded)
		})
	}
}

func TestVideoAssetHandlerConcurrentSlotsAreReleased(t *testing.T) {
	router, h, _ := videoAssetHandlerFixture(t, true)
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
	router, _, stub := videoAssetHandlerFixture(t, true)
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
