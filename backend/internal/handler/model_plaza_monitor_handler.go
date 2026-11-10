package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type modelPlazaMonitorBackend interface {
	GetConfig(context.Context) (*service.ModelPlazaMonitorConfig, error)
	UpdateConfig(context.Context, service.ModelPlazaMonitorConfig) (*service.ModelPlazaMonitorConfig, error)
	Candidates(context.Context) (*service.ModelPlazaMonitorCandidates, error)
	GetStatus(context.Context, int64) (*service.ModelPlazaMonitorStatusResponse, error)
	Run(context.Context, int64) (*service.ModelPlazaMonitorStatusResponse, error)
}

type modelPlazaMonitorCatalog interface {
	ListGroups(context.Context) ([]service.PlazaGroup, error)
}

type modelPlazaMonitorVisibility interface {
	GetUserGroupVisibility(context.Context, int64) (map[int64]struct{}, bool, error)
}

type modelPlazaMonitorSettings interface {
	GetModelPlazaRuntime(context.Context) service.ModelPlazaRuntime
}

// ModelPlazaMonitorHandler keeps monitor configuration and credentials on the admin surface.
type ModelPlazaMonitorHandler struct {
	monitor  modelPlazaMonitorBackend
	catalog  modelPlazaMonitorCatalog
	keys     modelPlazaMonitorVisibility
	settings modelPlazaMonitorSettings
}

func NewModelPlazaMonitorHandler(monitor *service.ModelPlazaMonitorService, catalog *service.ModelPlazaService, keys *service.APIKeyService, settings *service.SettingService) *ModelPlazaMonitorHandler {
	return &ModelPlazaMonitorHandler{monitor: monitor, catalog: catalog, keys: keys, settings: settings}
}

func (h *ModelPlazaMonitorHandler) GetConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := h.monitor.GetConfig(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *ModelPlazaMonitorHandler) UpdateConfig(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	var input service.ModelPlazaMonitorConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid model plaza monitor configuration")
		return
	}
	result, err := h.monitor.UpdateConfig(c.Request.Context(), input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *ModelPlazaMonitorHandler) Candidates(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := h.monitor.Candidates(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *ModelPlazaMonitorHandler) Run(c *gin.Context) {
	var input struct {
		GroupID int64 `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.GroupID <= 0 {
		response.BadRequest(c, "invalid group_id")
		return
	}
	result, err := h.monitor.Run(c.Request.Context(), input.GroupID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *ModelPlazaMonitorHandler) AdminStatus(c *gin.Context) {
	groupID, ok := modelPlazaMonitorGroupID(c)
	if !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	result, err := h.monitor.GetStatus(c.Request.Context(), groupID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

// Status uses the catalogue's visibility rules, including anonymous and subscription visibility.
// A read never schedules a probe, and an unauthorized group is indistinguishable from a missing one.
func (h *ModelPlazaMonitorHandler) Status(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if h.settings == nil || h.monitor == nil || h.catalog == nil {
		response.NotFound(c, "Model plaza is not enabled")
		return
	}
	runtime := h.settings.GetModelPlazaRuntime(c.Request.Context())
	if !runtime.Enabled {
		response.NotFound(c, "Model plaza is not enabled")
		return
	}
	subject, authed := middleware.GetAuthSubjectFromContext(c)
	if runtime.RequireAuth && !authed {
		response.Unauthorized(c, "Authentication required")
		return
	}
	groupID, ok := modelPlazaMonitorGroupID(c)
	if !ok {
		return
	}
	groups, err := h.catalog.ListGroups(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	var allowed map[int64]struct{}
	var restricted bool
	if authed {
		if h.keys == nil {
			response.Error(c, http.StatusServiceUnavailable, "group authorization unavailable")
			return
		}
		allowed, restricted, err = h.keys.GetUserGroupVisibility(c.Request.Context(), subject.UserID)
		if response.ErrorFrom(c, err) {
			return
		}
	}
	for _, group := range filterPlazaVisibleGroups(groups, allowed, restricted) {
		if group.ID != groupID {
			continue
		}
		result, statusErr := h.monitor.GetStatus(c.Request.Context(), groupID)
		if !response.ErrorFrom(c, statusErr) {
			response.Success(c, result)
		}
		return
	}
	response.NotFound(c, "group not found")
}

func modelPlazaMonitorGroupID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid group_id")
		return 0, false
	}
	return id, true
}
