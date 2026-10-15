package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *RequestAuditHandler) ListDeviceBans(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	limit, ok := requestAuditLimit(c)
	if !ok {
		return
	}
	state := c.Query("state")
	if state != "" && state != "active" && state != "expired" && state != "revoked" {
		requestAuditInvalid(c, "Invalid ban state")
		return
	}
	filter := requestaudit.DeviceBanFilter{Limit: limit, DeviceType: c.Query("device_type"), DeviceID: c.Query("device_id"), SourceAuditID: c.Query("source_audit_id"), State: state, Cursor: c.Query("cursor")}
	result, err := h.store.ListDeviceBans(c.Request.Context(), filter)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) GetDeviceBan(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	result, err := h.store.GetDeviceBan(c.Request.Context(), id)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) CreateDeviceBan(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	var input requestaudit.CreateDeviceBanInput
	if c.ShouldBindJSON(&input) != nil {
		requestAuditInvalid(c, "Invalid device ban")
		return
	}
	if _, err := uuid.Parse(input.SourceAuditID); err != nil {
		requestAuditInvalid(c, "Invalid source_audit_id")
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	input.CreatedBy = subject.UserID
	result, err := h.store.CreateDeviceBan(c.Request.Context(), input)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"audit_id": input.SourceAuditID, "ban_id": result.ID})
	response.Created(c, result)
}

func (h *RequestAuditHandler) RevokeDeviceBan(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	result, err := h.store.RevokeDeviceBan(c.Request.Context(), id, subject.UserID)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"ban_id": id})
	response.Success(c, result)
}
