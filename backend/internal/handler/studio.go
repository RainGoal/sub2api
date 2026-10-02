package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type StudioHandler struct{ service *service.StudioService }

func NewStudioHandler(repo service.StudioRepository, keys service.APIKeyRepository, images *service.ImageTaskService, videos service.SeedanceVideoTaskRepository) *StudioHandler {
	return &StudioHandler{service: service.NewStudioService(repo, keys, images, videos)}
}

func (h *StudioHandler) List(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, sizeErr := strconv.Atoi(c.DefaultQuery("page_size", "100"))
	if pageErr != nil || sizeErr != nil {
		response.ErrorFrom(c, service.ErrStudioInvalidTask)
		return
	}
	items, hasMore, err := h.service.List(c.Request.Context(), subject.UserID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"items": items, "page": page, "page_size": pageSize, "has_more": hasMore})
}

func (h *StudioHandler) Save(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.StudioMaxRecordBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var task service.StudioTask
	err := decoder.Decode(&task)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.ErrorWithDetails(c, 413, "Studio task record is too large", "STUDIO_RECORD_TOO_LARGE", nil)
		return
	}
	if err != nil || decoder.Decode(new(any)) != io.EOF || task.ID != c.Param("id") {
		response.ErrorFrom(c, service.ErrStudioInvalidTask)
		return
	}
	saved, err := h.service.Save(c.Request.Context(), subject.UserID, task)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, saved)
}
