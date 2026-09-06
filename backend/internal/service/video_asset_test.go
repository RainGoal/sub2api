package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type videoAssetSettingsRepo struct {
	SettingRepository
	value string
}

func (r *videoAssetSettingsRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}
func (r *videoAssetSettingsRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

type videoAssetEncryptor struct{}

func (videoAssetEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (videoAssetEncryptor) Decrypt(value string) (string, error) { return value, nil }

type videoAssetRecordingStorage struct {
	key, contentType string
	data             []byte
	fail             bool
}

func (s *videoAssetRecordingStorage) Save(_ context.Context, key, contentType string, file io.ReadSeeker, size int64) (string, *time.Time, error) {
	if s.fail {
		return "", nil, errors.New("upstream contains private credentials")
	}
	s.key, s.contentType = key, contentType
	s.data, _ = io.ReadAll(file)
	expires := time.Now().Add(24 * time.Hour)
	return "https://storage.example.com/" + key + "?signature=test", &expires, nil
}

func videoAssetFixture(t *testing.T) (*VideoAssetService, *ImageStorageSettingService, *videoAssetRecordingStorage, *videoAssetSettingsRepo) {
	t.Helper()
	settings := ImageStorageSettings{VideoUploadEnabled: true, Endpoint: "https://account.r2.cloudflarestorage.com", Bucket: "media", AccessKeyID: "id", SecretAccessKey: "secret", PublicBaseURL: "https://public.example.com"}
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	repo := &videoAssetSettingsRepo{value: string(raw)}
	settingSvc := NewImageStorageSettingService(repo, videoAssetEncryptor{}, nil, nil, config.ImageStorageConfig{})
	storage := &videoAssetRecordingStorage{}
	factory := func(_ context.Context, cfg *config.ImageStorageConfig) (VideoAssetStorage, error) {
		require.Empty(t, cfg.PublicBaseURL, "input uploads must keep signed reads even when images use a public URL")
		require.Equal(t, "secret", cfg.SecretAccessKey)
		return storage, nil
	}
	return NewVideoAssetService(settingSvc, factory), settingSvc, storage, repo
}

func videoAssetPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return data.Bytes()
}

func TestVideoAssetUploadUsesPrivateNamespacedStorage(t *testing.T) {
	svc, _, storage, _ := videoAssetFixture(t)
	data := videoAssetPNG(t)
	asset, err := svc.Upload(context.Background(), 42, "image", bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Equal(t, data, storage.data)
	require.Equal(t, "image/png", storage.contentType)
	require.Regexp(t, `^video-inputs/\d{4}/\d{2}/\d{2}/42/[a-f0-9]{32}\.png$`, storage.key)
	require.Equal(t, int64(len(data)), asset.Size)
	require.NotNil(t, asset.URLExpiresAt)
	firstKey := storage.key
	_, err = svc.Upload(context.Background(), 42, "image", bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.NotEqual(t, firstKey, storage.key)
}

func TestVideoAssetToggleIsIndependentAndTakesEffectImmediately(t *testing.T) {
	svc, settings, _, repo := videoAssetFixture(t)
	cfg, err := svc.Config(context.Background())
	require.NoError(t, err)
	require.True(t, cfg.Enabled, "async image switch is false in this fixture")
	current, err := settings.Get(context.Background())
	require.NoError(t, err)
	current.VideoUploadEnabled = false
	_, err = settings.Update(context.Background(), *current)
	require.NoError(t, err)
	cfg, err = svc.Config(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	data := videoAssetPNG(t)
	_, err = svc.Upload(context.Background(), 42, "image", bytes.NewReader(data), int64(len(data)))
	require.Equal(t, 403, apperrors.Code(err))
	repo.value = ""
	cfg, err = svc.Config(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.Enabled, "never configured means off")
}

func TestVideoAssetLimitsPersistAndTakeEffectWithoutRestart(t *testing.T) {
	svc, settings, _, repo := videoAssetFixture(t)
	ctx := context.Background()
	initial, err := svc.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, 20, initial.MaxUploadsPerMinute)
	require.Equal(t, int64(1<<30), initial.DailyLimitBytes)
	for _, limits := range []struct {
		perMinute int
		dailyMiB  int64
	}{{40, 2048}, {10, 512}} {
		current, err := settings.Get(ctx)
		require.NoError(t, err)
		current.VideoUploadMaxPerMinute = limits.perMinute
		current.VideoUploadDailyLimitMiB = limits.dailyMiB
		saved, err := settings.Update(ctx, *current)
		require.NoError(t, err)
		require.Equal(t, limits.perMinute, saved.VideoUploadMaxPerMinute)
		require.Equal(t, limits.dailyMiB, saved.VideoUploadDailyLimitMiB)
		var stored ImageStorageSettings
		require.NoError(t, json.Unmarshal([]byte(repo.value), &stored))
		require.Equal(t, limits.perMinute, stored.VideoUploadMaxPerMinute)
		require.Equal(t, limits.dailyMiB, stored.VideoUploadDailyLimitMiB)
		cfg, err := svc.Config(ctx)
		require.NoError(t, err)
		require.True(t, cfg.Enabled)
		require.Equal(t, limits.perMinute, cfg.MaxUploadsPerMinute)
		require.Equal(t, limits.dailyMiB<<20, cfg.DailyLimitBytes)
	}
	require.Equal(t, 20, initial.MaxUploadsPerMinute, "an in-flight request keeps its configuration snapshot")
	require.Equal(t, int64(1<<30), initial.DailyLimitBytes)
}

func TestVideoAssetLimitsDefaultForLegacyAndUnconfiguredSettings(t *testing.T) {
	for _, raw := range []string{"", `{}`, `{"video_upload_enabled":false}`} {
		t.Run(raw, func(t *testing.T) {
			svc, settings, _, repo := videoAssetFixture(t)
			repo.value = raw
			current, err := settings.Get(context.Background())
			require.NoError(t, err)
			require.Equal(t, 20, current.VideoUploadMaxPerMinute)
			require.Equal(t, int64(1024), current.VideoUploadDailyLimitMiB)
			cfg, err := svc.Config(context.Background())
			require.NoError(t, err)
			require.False(t, cfg.Enabled)
			require.Equal(t, 20, cfg.MaxUploadsPerMinute)
			require.Equal(t, int64(1<<30), cfg.DailyLimitBytes)
			// Older admin clients that omit the new fields remain compatible.
			saved, err := settings.Update(context.Background(), ImageStorageSettings{})
			require.NoError(t, err)
			require.Equal(t, 20, saved.VideoUploadMaxPerMinute)
			require.Equal(t, int64(1024), saved.VideoUploadDailyLimitMiB)
		})
	}
}

func TestVideoAssetLimitsRejectInvalidSettingsWithoutSaving(t *testing.T) {
	for _, tc := range []struct {
		name      string
		perMinute int
		dailyMiB  int64
	}{
		{"negative rate", -1, 1024},
		{"excessive rate", 10_001, 1024},
		{"negative capacity", 20, -1},
		{"excessive capacity", 20, (1 << 20) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, settings, _, repo := videoAssetFixture(t)
			original := repo.value
			_, err := settings.Update(context.Background(), ImageStorageSettings{
				VideoUploadMaxPerMinute:  tc.perMinute,
				VideoUploadDailyLimitMiB: tc.dailyMiB,
			})
			require.Equal(t, 400, apperrors.Code(err))
			require.Equal(t, "INVALID_VIDEO_UPLOAD_LIMIT", apperrors.Reason(err))
			require.Equal(t, original, repo.value)
		})
	}
}

func TestVideoAssetRejectsInvalidContentAndSize(t *testing.T) {
	svc, _, storage, _ := videoAssetFixture(t)
	pngData := videoAssetPNG(t)
	for _, tc := range []struct {
		name, media string
		data        []byte
		size        int64
		code        int
	}{
		{"empty", "image", nil, 0, 400},
		{"unknown type", "document", pngData, int64(len(pngData)), 400},
		{"spoofed image", "image", []byte("<html>not an image</html>"), 25, 415},
		{"wrong type", "video", pngData, int64(len(pngData)), 415},
		{"truncated image", "image", pngData[:16], 16, 415},
		{"wrong size", "image", pngData, int64(len(pngData) + 1), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "spoofed image" {
				tc.size = int64(len(tc.data))
			}
			_, err := svc.Upload(context.Background(), 42, tc.media, bytes.NewReader(tc.data), tc.size)
			require.Equal(t, tc.code, apperrors.Code(err))
			require.Empty(t, storage.key)
		})
	}
	large := io.NewSectionReader(strings.NewReader("x"), 0, 21<<20)
	_, err := svc.Upload(context.Background(), 42, "image", large, 21<<20)
	require.Equal(t, 413, apperrors.Code(err))
}

func TestVideoAssetStorageErrorDoesNotExposeCredentials(t *testing.T) {
	svc, _, storage, _ := videoAssetFixture(t)
	storage.fail = true
	data := videoAssetPNG(t)
	_, err := svc.Upload(context.Background(), 42, "image", bytes.NewReader(data), int64(len(data)))
	require.Equal(t, 502, apperrors.Code(err))
	require.NotContains(t, err.Error(), "private credentials")
}
