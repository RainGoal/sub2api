package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	_ "golang.org/x/image/webp"
)

const VideoAssetMaxBytes int64 = 50 << 20

type VideoAssetLimit struct {
	MaxBytes  int64    `json:"max_bytes"`
	MIMETypes []string `json:"mime_types"`
}

type VideoAssetConfig struct {
	Enabled             bool                       `json:"enabled"`
	Limits              map[string]VideoAssetLimit `json:"limits"`
	MaxUploadsPerMinute int                        `json:"max_uploads_per_minute"`
	DailyLimitBytes     int64                      `json:"daily_limit_bytes"`
}

type VideoAsset struct {
	URL          string     `json:"url"`
	MediaType    string     `json:"media_type"`
	Size         int64      `json:"size"`
	URLExpiresAt *time.Time `json:"url_expires_at"`
}

// VideoAssetStorage keeps input uploads independent of image result rewriting.
type VideoAssetStorage interface {
	Save(context.Context, string, string, io.ReadSeeker, int64) (string, *time.Time, error)
}

type VideoAssetStorageFactory func(context.Context, *config.ImageStorageConfig) (VideoAssetStorage, error)

type VideoAssetService struct {
	settings *ImageStorageSettingService
	factory  VideoAssetStorageFactory
}

func NewVideoAssetService(settings *ImageStorageSettingService, factory VideoAssetStorageFactory) *VideoAssetService {
	return &VideoAssetService{settings: settings, factory: factory}
}

func videoAssetLimits() map[string]VideoAssetLimit {
	return map[string]VideoAssetLimit{
		"image": {20 << 20, []string{"image/jpeg", "image/png", "image/webp"}},
		"video": {VideoAssetMaxBytes, []string{"video/mp4", "video/webm"}},
		"audio": {20 << 20, []string{"audio/mpeg", "audio/wav", "audio/ogg", "audio/mp4"}},
	}
}

func (s *VideoAssetService) storageConfig(ctx context.Context) (*config.ImageStorageConfig, *ImageStorageSettings, error) {
	if s == nil || s.settings == nil || s.factory == nil {
		return nil, nil, nil
	}
	settings, err := s.settings.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	if settings == nil || !settings.VideoUploadEnabled {
		return nil, settings, nil
	}
	cfg, err := s.settings.toImageStorageConfig(ctx, settings)
	if err != nil {
		return nil, settings, err
	}
	if !cfg.IsConfigured() {
		return nil, settings, nil
	}
	if cfg.Endpoint != "" {
		endpoint, parseErr := url.Parse(cfg.Endpoint)
		if parseErr != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil {
			return nil, settings, apperrors.New(503, "VIDEO_ASSET_STORAGE_UNAVAILABLE", "Media storage requires an HTTPS endpoint")
		}
	}
	// Uploaded inputs always use signed reads; enabling this feature never
	// requires publishing a bucket that may also contain database backups.
	cfg.PublicBaseURL = ""
	return cfg, settings, nil
}

func (s *VideoAssetService) Config(ctx context.Context) (*VideoAssetConfig, error) {
	cfg, settings, err := s.storageConfig(ctx)
	if err != nil {
		return nil, apperrors.New(503, "VIDEO_ASSET_STORAGE_UNAVAILABLE", "Media storage is unavailable")
	}
	result := &VideoAssetConfig{
		Enabled: cfg != nil, Limits: videoAssetLimits(),
		MaxUploadsPerMinute: DefaultVideoUploadMaxPerMinute,
		DailyLimitBytes:     DefaultVideoUploadDailyLimitMiB << 20,
	}
	if settings != nil {
		result.MaxUploadsPerMinute = settings.VideoUploadMaxPerMinute
		result.DailyLimitBytes = settings.VideoUploadDailyLimitMiB << 20
	}
	return result, nil
}

func (s *VideoAssetService) Upload(ctx context.Context, userID int64, mediaType string, file io.ReadSeeker, size int64) (*VideoAsset, error) {
	if userID <= 0 {
		return nil, apperrors.New(401, "UNAUTHORIZED", "Authentication required")
	}
	limit, ok := videoAssetLimits()[mediaType]
	if !ok {
		return nil, apperrors.New(400, "INVALID_MEDIA_TYPE", "Choose image, video, or audio")
	}
	if file == nil {
		return nil, apperrors.New(400, "INVALID_UPLOAD", "The uploaded file is empty or incomplete")
	}
	actualSize, err := file.Seek(0, io.SeekEnd)
	if err != nil || actualSize != size || size <= 0 {
		return nil, apperrors.New(400, "INVALID_UPLOAD", "The uploaded file is empty or incomplete")
	}
	if size > limit.MaxBytes {
		return nil, apperrors.New(413, "FILE_TOO_LARGE", "The file exceeds the upload size limit")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	contentType, extension, err := validateVideoAsset(file, mediaType, limit.MIMETypes)
	if err != nil {
		return nil, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	cfg, _, err := s.storageConfig(ctx)
	if err != nil {
		return nil, apperrors.New(503, "VIDEO_ASSET_STORAGE_UNAVAILABLE", "Media storage is unavailable")
	}
	if cfg == nil {
		return nil, apperrors.New(403, "VIDEO_ASSET_UPLOAD_DISABLED", "Media uploads are not enabled")
	}
	storage, err := s.factory(ctx, cfg)
	if err != nil {
		return nil, apperrors.New(503, "VIDEO_ASSET_STORAGE_UNAVAILABLE", "Media storage is unavailable")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	key := "video-inputs/" + time.Now().UTC().Format("2006/01/02") + "/" + strconv.FormatInt(userID, 10) + "/" + hex.EncodeToString(random[:]) + extension
	assetURL, expiresAt, err := storage.Save(ctx, key, contentType, file, size)
	if err != nil {
		return nil, apperrors.New(502, "VIDEO_ASSET_UPLOAD_FAILED", "Could not save the file; please retry")
	}
	parsed, err := url.Parse(assetURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, apperrors.New(503, "VIDEO_ASSET_STORAGE_UNAVAILABLE", "Media storage must provide HTTPS links")
	}
	return &VideoAsset{URL: assetURL, MediaType: mediaType, Size: size, URLExpiresAt: expiresAt}, nil
}

func validateVideoAsset(file io.ReadSeeker, mediaType string, allowed []string) (string, string, error) {
	var header [512]byte
	n, _ := io.ReadFull(file, header[:])
	contentType := http.DetectContentType(header[:n])
	if contentType == "application/ogg" {
		contentType = "audio/ogg"
	}
	if contentType == "audio/wave" || contentType == "audio/x-wav" {
		contentType = "audio/wav"
	}
	// ISO BMFF distinguishes audio-only M4A from an MP4 video container.
	if n >= 12 && string(header[4:8]) == "ftyp" && string(header[8:12]) == "M4A " {
		contentType = "audio/mp4"
	}
	valid := false
	for _, candidate := range allowed {
		valid = valid || candidate == contentType
	}
	if !valid {
		return "", "", apperrors.New(415, "UNSUPPORTED_MEDIA_TYPE", "The file content does not match a supported media format")
	}
	if mediaType == "image" {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", "", err
		}
		cfg, _, err := image.DecodeConfig(file)
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100_000_000 {
			return "", "", apperrors.New(415, "UNSUPPORTED_MEDIA_TYPE", "The image is invalid or its dimensions are too large")
		}
	}
	extensions := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "video/mp4": ".mp4", "video/webm": ".webm", "audio/mpeg": ".mp3", "audio/wav": ".wav", "audio/ogg": ".ogg", "audio/mp4": ".m4a"}
	return strings.TrimSpace(contentType), extensions[contentType], nil
}
