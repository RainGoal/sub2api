package repository

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestVideoAssetStorageStreamsAndSignsReadURL(t *testing.T) {
	payload := []byte("bounded video file")
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/media/video-inputs/test.mp4", r.URL.Path)
		require.Equal(t, "video/mp4", r.Header.Get("Content-Type"))
		require.Equal(t, int64(len(payload)), r.ContentLength)
		var err error
		received, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	store, err := ProvideVideoAssetStorageFactory()(context.Background(), &config.ImageStorageConfig{
		Endpoint: server.URL, Bucket: "media", Region: "auto", AccessKeyID: "test-id", SecretAccessKey: "test-secret",
		ForcePathStyle: true, PublicBaseURL: "https://public.example.com", PresignExpiry: 1,
	})
	require.NoError(t, err)
	assetURL, expires, err := store.Save(context.Background(), "video-inputs/test.mp4", "video/mp4", bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, payload, received)
	parsed, err := url.Parse(assetURL)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
	require.Equal(t, "86400", parsed.Query().Get("X-Amz-Expires"))
	require.NotContains(t, assetURL, "public.example.com")
	require.WithinDuration(t, time.Now().Add(24*time.Hour), *expires, time.Minute)
}

func TestVideoAssetStorageCapsSignatureLifetime(t *testing.T) {
	store, err := ProvideVideoAssetStorageFactory()(context.Background(), &config.ImageStorageConfig{
		Endpoint: "https://account.r2.cloudflarestorage.com", Bucket: "media", Region: "auto",
		AccessKeyID: "test-id", SecretAccessKey: "test-secret", PresignExpiry: 9999,
	})
	require.NoError(t, err)
	require.Equal(t, 7*24*time.Hour, store.(*S3VideoAssetStorage).storage.presignExpiry)
}
