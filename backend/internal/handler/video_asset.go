package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type videoAssetService interface {
	Config(context.Context) (*service.VideoAssetConfig, error)
	Upload(context.Context, int64, string, io.ReadSeeker, int64) (*service.VideoAsset, error)
}

type VideoAssetHandler struct {
	service videoAssetService
	limiter service.VideoAssetUploadLimiter
	slots   chan struct{}
	mu      sync.Mutex
	active  map[int64]int
}

func NewVideoAssetHandler(assets *service.VideoAssetService, limiter service.VideoAssetUploadLimiter) *VideoAssetHandler {
	return &VideoAssetHandler{
		service: assets, limiter: limiter,
		slots: make(chan struct{}, 8), active: make(map[int64]int),
	}
}

func (h *VideoAssetHandler) Config(c *gin.Context) {
	if subject, ok := middleware.GetAuthSubjectFromContext(c); !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	cfg, err := h.service.Config(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// Upload accepts one file per request. A small multipart memory budget and
// bounded concurrent requests cap temporary memory/disk use for large videos.
func (h *VideoAssetHandler) Upload(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	cfg, err := h.service.Config(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !cfg.Enabled {
		response.ErrorWithDetails(c, 403, "Media uploads are not enabled", "VIDEO_ASSET_UPLOAD_DISABLED", nil)
		return
	}
	if !h.allowUpload(c, subject.UserID, cfg.MaxUploadsPerMinute) {
		return
	}
	if !h.acquire(subject.UserID) {
		c.Header("Retry-After", "3")
		response.ErrorWithDetails(c, 429, "Other uploads are in progress; please retry", "UPLOAD_BUSY", nil)
		return
	}
	defer h.release(subject.UserID)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.VideoAssetMaxBytes+(1<<20))
	err = c.Request.ParseMultipartForm(1 << 20)
	if c.Request.MultipartForm != nil {
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.ErrorWithDetails(c, 413, "The file exceeds the upload size limit", "FILE_TOO_LARGE", nil)
		} else {
			response.ErrorWithDetails(c, 400, "Send one file as multipart/form-data", "INVALID_UPLOAD", nil)
		}
		return
	}
	form := c.Request.MultipartForm
	files, types := form.File["file"], form.Value["media_type"]
	if len(form.File) != 1 || len(files) != 1 || len(types) != 1 {
		response.ErrorWithDetails(c, 400, "Send one file and its media_type", "INVALID_UPLOAD", nil)
		return
	}
	limit, ok := cfg.Limits[types[0]]
	if !ok {
		response.ErrorWithDetails(c, 400, "Choose image, video, or audio", "INVALID_MEDIA_TYPE", nil)
		return
	}
	if files[0].Size > limit.MaxBytes {
		response.ErrorWithDetails(c, 413, "The file exceeds the upload size limit", "FILE_TOO_LARGE", nil)
		return
	}
	if !h.reserveBytes(c, subject.UserID, files[0].Size, cfg.DailyLimitBytes) {
		return
	}
	file, err := files[0].Open()
	if err != nil {
		response.ErrorWithDetails(c, 400, "Could not read the uploaded file", "INVALID_UPLOAD", nil)
		return
	}
	defer func() { _ = file.Close() }()
	asset, err := h.service.Upload(ctx, subject.UserID, types[0], file, files[0].Size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, asset)
}

func (h *VideoAssetHandler) acquire(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active[userID] >= 2 {
		return false
	}
	select {
	case h.slots <- struct{}{}:
		h.active[userID]++
		return true
	default:
		return false
	}
}

func (h *VideoAssetHandler) release(userID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	<-h.slots
	h.active[userID]--
	if h.active[userID] == 0 {
		delete(h.active, userID)
	}
}

func (h *VideoAssetHandler) allowUpload(c *gin.Context, userID int64, maxPerMinute int) bool {
	if h.limiter == nil {
		response.ErrorWithDetails(c, 503, "Upload limits are temporarily unavailable", "UPLOAD_LIMIT_UNAVAILABLE", nil)
		return false
	}
	allowed, retryAfter, err := h.limiter.Allow(c.Request.Context(), userID, maxPerMinute)
	if err != nil {
		response.ErrorWithDetails(c, 503, "Upload limits are temporarily unavailable", "UPLOAD_LIMIT_UNAVAILABLE", nil)
		return false
	}
	if !allowed {
		c.Header("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
		response.ErrorWithDetails(c, 429, "Too many uploads; please retry later", "UPLOAD_RATE_LIMITED", nil)
		return false
	}
	return true
}

func (h *VideoAssetHandler) reserveBytes(c *gin.Context, userID, size, dailyLimit int64) bool {
	allowed, err := h.limiter.ReserveBytes(c.Request.Context(), userID, size, dailyLimit)
	if err != nil {
		response.ErrorWithDetails(c, 503, "Upload limits are temporarily unavailable", "UPLOAD_LIMIT_UNAVAILABLE", nil)
		return false
	}
	if !allowed {
		response.ErrorWithDetails(c, 429, "Daily upload capacity reached; please try tomorrow", "UPLOAD_QUOTA_EXCEEDED", nil)
		return false
	}
	return true
}
