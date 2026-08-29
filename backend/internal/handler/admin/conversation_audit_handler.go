package admin

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/conversationaudit"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	conversationAuditReadTimeout   = 5 * time.Second
	conversationAuditDeleteTimeout = 10 * time.Second
	conversationAuditDefaultList   = 50
	conversationAuditMaxList       = 100
	conversationAuditDateLayout    = "2006-01-02"
)

type conversationAuditAdminService interface {
	Config() (conversationaudit.PublicConfig, error)
	SaveConfig(context.Context, conversationaudit.UpdateConfigRequest, int64) (conversationaudit.PublicConfig, error)
	Runtime() conversationaudit.RuntimeState
	List(context.Context, conversationaudit.ListFilter, string) (conversationaudit.RecordPage, error)
	Detail(context.Context, time.Time, uuid.UUID) (conversationaudit.RecordDetailView, error)
	DeleteOne(context.Context, time.Time, uuid.UUID) error
	PreviewDelete(context.Context, conversationaudit.ListFilter, int64) (conversationaudit.DeletePreviewView, error)
	DeleteByToken(context.Context, string, int64) (conversationaudit.DeleteResult, error)
}

type ConversationAuditHandler struct {
	service conversationAuditAdminService
}

func NewConversationAuditHandler(service *conversationaudit.AdminService) *ConversationAuditHandler {
	return &ConversationAuditHandler{service: service}
}

func (h *ConversationAuditHandler) GetConfig(c *gin.Context) {
	config, err := h.service.Config()
	if err != nil {
		writeConversationAuditError(c, err)
		return
	}
	response.Success(c, config)
}

func (h *ConversationAuditHandler) UpdateConfig(c *gin.Context) {
	var request conversationaudit.UpdateConfigRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		setConversationAuditOperation(c, "failed", conversationaudit.ErrorCodeInvalidConfig, nil)
		writeConversationAuditError(c, conversationaudit.ErrInvalidConfig)
		return
	}
	config, err := h.service.SaveConfig(c.Request.Context(), request, conversationAuditAdminID(c))
	if err != nil {
		setConversationAuditOperation(c, "failed", conversationAuditReason(err), map[string]any{
			"enabled": request.Enabled, "config_version": request.ExpectedConfigVersion,
		})
		writeConversationAuditError(c, err)
		return
	}
	setConversationAuditOperation(c, "success", "", map[string]any{
		"enabled": config.Enabled, "config_version": config.ConfigVersion,
	})
	response.Success(c, config)
}

func (h *ConversationAuditHandler) GetRuntime(c *gin.Context) {
	response.Success(c, h.service.Runtime())
}

func (h *ConversationAuditHandler) ListRecords(c *gin.Context) {
	filter, err := conversationAuditFilterFromQuery(c)
	if err != nil {
		writeConversationAuditError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), conversationAuditReadTimeout)
	defer cancel()
	page, err := h.service.List(ctx, filter, c.Query("cursor"))
	if err != nil {
		writeConversationAuditError(c, err)
		return
	}
	response.Success(c, page)
}

func (h *ConversationAuditHandler) GetRecord(c *gin.Context) {
	day, auditID, err := conversationAuditRecordIdentity(c)
	if err != nil {
		writeConversationAuditError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"audit_id": auditID.String()})
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	ctx, cancel := context.WithTimeout(c.Request.Context(), conversationAuditReadTimeout)
	defer cancel()
	detail, err := h.service.Detail(ctx, day, auditID)
	if err != nil {
		writeConversationAuditError(c, err)
		return
	}
	response.Success(c, detail)
}

func (h *ConversationAuditHandler) DeleteRecord(c *gin.Context) {
	day, auditID, err := conversationAuditRecordIdentity(c)
	if err != nil {
		setConversationAuditOperation(c, "failed", conversationaudit.ErrorCodeRecordNotFound, nil)
		writeConversationAuditError(c, err)
		return
	}
	fields := map[string]any{"audit_id": auditID.String(), "requested_count": 1}
	ctx, cancel := context.WithTimeout(c.Request.Context(), conversationAuditDeleteTimeout)
	defer cancel()
	if err := h.service.DeleteOne(ctx, day, auditID); err != nil {
		setConversationAuditOperation(c, "failed", conversationAuditReason(err), fields)
		writeConversationAuditError(c, err)
		return
	}
	fields["deleted_records"] = 1
	setConversationAuditOperation(c, "success", "", fields)
	response.Success(c, conversationaudit.DeleteResult{DeletedRecords: 1})
}

func (h *ConversationAuditHandler) DeletePreview(c *gin.Context) {
	var request conversationAuditFilterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		setConversationAuditOperation(c, "failed", conversationaudit.ErrorCodeDeleteTimeRangeRequired, nil)
		writeConversationAuditError(c, conversationaudit.ErrTimeRangeRequired)
		return
	}
	filter, err := request.filter()
	if err != nil {
		setConversationAuditOperation(c, "failed", conversationAuditReason(err), nil)
		writeConversationAuditError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), conversationAuditReadTimeout)
	defer cancel()
	preview, err := h.service.PreviewDelete(ctx, filter, conversationAuditAdminID(c))
	if err != nil {
		setConversationAuditOperation(c, "failed", conversationAuditReason(err), nil)
		writeConversationAuditError(c, err)
		return
	}
	setConversationAuditOperation(c, "success", "", map[string]any{
		"matched_count": preview.MatchedCount, "filter_hash": preview.FilterHash,
		"operation_type": preview.OperationType,
	})
	response.Success(c, preview)
}

type conversationAuditDeleteConfirmRequest struct {
	ConfirmationToken string `json:"confirmation_token" binding:"required"`
	Confirm           bool   `json:"confirm" binding:"required"`
}

func (h *ConversationAuditHandler) DeleteByFilter(c *gin.Context) {
	var request conversationAuditDeleteConfirmRequest
	if err := c.ShouldBindJSON(&request); err != nil || !request.Confirm {
		setConversationAuditOperation(c, "failed", conversationaudit.ErrorCodeDeleteConfirmationInvalid, nil)
		writeConversationAuditError(c, conversationaudit.ErrInvalidDeleteConfirmation)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), conversationAuditDeleteTimeout)
	defer cancel()
	result, err := h.service.DeleteByToken(ctx, request.ConfirmationToken, conversationAuditAdminID(c))
	if err != nil {
		setConversationAuditOperation(c, "failed", conversationAuditReason(err), nil)
		writeConversationAuditError(c, err)
		return
	}
	setConversationAuditOperation(c, "success", "", map[string]any{"deleted_records": result.DeletedRecords})
	response.Success(c, result)
}

type conversationAuditFilterRequest struct {
	Start           time.Time                       `json:"start"`
	End             time.Time                       `json:"end"`
	UserID          *int64                          `json:"user_id"`
	GroupID         *int64                          `json:"group_id"`
	APIKeyID        *int64                          `json:"api_key_id"`
	SessionID       string                          `json:"session_id"`
	RequestID       string                          `json:"request_id"`
	OutcomeStatus   conversationaudit.OutcomeStatus `json:"outcome_status"`
	CaptureStatus   conversationaudit.CaptureStatus `json:"capture_status"`
	Protocol        string                          `json:"protocol"`
	InboundEndpoint string                          `json:"inbound_endpoint"`
	RequestedModel  string                          `json:"requested_model"`
}

func (r conversationAuditFilterRequest) filter() (conversationaudit.ListFilter, error) {
	filter := conversationaudit.ListFilter{
		Start: r.Start, End: r.End, UserID: r.UserID, GroupID: r.GroupID, APIKeyID: r.APIKeyID,
		SessionID: r.SessionID, RequestID: r.RequestID, OutcomeStatus: r.OutcomeStatus,
		CaptureStatus: r.CaptureStatus, Protocol: r.Protocol, InboundEndpoint: r.InboundEndpoint,
		RequestedModel: r.RequestedModel,
	}
	if err := validateConversationAuditFilterValues(filter); err != nil {
		return conversationaudit.ListFilter{}, err
	}
	return filter, nil
}

func conversationAuditFilterFromQuery(c *gin.Context) (conversationaudit.ListFilter, error) {
	start, err := requiredConversationAuditTime(c.Query("start"))
	if err != nil {
		return conversationaudit.ListFilter{}, err
	}
	end, err := requiredConversationAuditTime(c.Query("end"))
	if err != nil {
		return conversationaudit.ListFilter{}, err
	}
	userID, err := optionalConversationAuditID(c.Query("user_id"))
	if err != nil {
		return conversationaudit.ListFilter{}, err
	}
	groupID, err := optionalConversationAuditID(c.Query("group_id"))
	if err != nil {
		return conversationaudit.ListFilter{}, err
	}
	apiKeyID, err := optionalConversationAuditID(c.Query("api_key_id"))
	if err != nil {
		return conversationaudit.ListFilter{}, err
	}
	limit := conversationAuditDefaultList
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > conversationAuditMaxList {
			return conversationaudit.ListFilter{}, conversationaudit.ErrQueryLimit
		}
	}
	filter := conversationaudit.ListFilter{
		Start: start, End: end, UserID: userID, GroupID: groupID, APIKeyID: apiKeyID,
		SessionID: c.Query("session_id"), RequestID: c.Query("request_id"),
		OutcomeStatus: conversationaudit.OutcomeStatus(c.Query("outcome_status")),
		CaptureStatus: conversationaudit.CaptureStatus(c.Query("capture_status")),
		Protocol:      c.Query("protocol"), InboundEndpoint: c.Query("inbound_endpoint"),
		RequestedModel: c.Query("requested_model"), Limit: limit,
	}
	if err := validateConversationAuditFilterValues(filter); err != nil {
		return conversationaudit.ListFilter{}, err
	}
	return filter, nil
}

func validateConversationAuditFilterValues(filter conversationaudit.ListFilter) error {
	for _, id := range []*int64{filter.UserID, filter.GroupID, filter.APIKeyID} {
		if id != nil && *id <= 0 {
			return conversationaudit.ErrQueryLimit
		}
	}
	switch filter.OutcomeStatus {
	case "", conversationaudit.OutcomeCompleted, conversationaudit.OutcomeError, conversationaudit.OutcomeTimeout,
		conversationaudit.OutcomePartial, conversationaudit.OutcomeCancelled, conversationaudit.OutcomeUnknown:
	default:
		return conversationaudit.ErrQueryLimit
	}
	switch filter.CaptureStatus {
	case "", conversationaudit.CaptureComplete, conversationaudit.CaptureTruncated,
		conversationaudit.CaptureMetadataOnly, conversationaudit.CaptureDegraded:
	default:
		return conversationaudit.ErrQueryLimit
	}
	return nil
}

func requiredConversationAuditTime(raw string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, conversationaudit.ErrQueryLimit
	}
	return value, nil
}

func optionalConversationAuditID(raw string) (*int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return nil, conversationaudit.ErrQueryLimit
	}
	return &value, nil
}

func conversationAuditRecordIdentity(c *gin.Context) (time.Time, uuid.UUID, error) {
	day, err := time.ParseInLocation(conversationAuditDateLayout, strings.TrimSpace(c.Param("date")), time.UTC)
	if err != nil {
		return time.Time{}, uuid.Nil, conversationaudit.ErrRecordNotFound
	}
	auditID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil || auditID == uuid.Nil {
		return time.Time{}, uuid.Nil, conversationaudit.ErrRecordNotFound
	}
	return day, auditID, nil
}

func conversationAuditAdminID(c *gin.Context) int64 {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		return 0
	}
	return subject.UserID
}

func setConversationAuditOperation(c *gin.Context, result, errorCode string, fields map[string]any) {
	details := map[string]any{"result": result}
	if errorCode != "" {
		details["error_code"] = errorCode
	}
	for key, value := range fields {
		details[key] = value
	}
	middleware.SetAuditExtra(c, details)
}

func conversationAuditReason(err error) string {
	switch {
	case errors.Is(err, conversationaudit.ErrInvalidConfig):
		return conversationaudit.ErrorCodeInvalidConfig
	case errors.Is(err, conversationaudit.ErrConfigConflict):
		return conversationaudit.ErrorCodeConfigConflict
	case errors.Is(err, conversationaudit.ErrEncryptionUnavailable):
		return conversationaudit.ErrorCodeEncryptionUnavailable
	case errors.Is(err, conversationaudit.ErrRecordNotFound):
		return conversationaudit.ErrorCodeRecordNotFound
	case errors.Is(err, conversationaudit.ErrRecordBusy):
		return conversationaudit.ErrorCodeRecordBusy
	case errors.Is(err, conversationaudit.ErrInvalidCursor):
		return conversationaudit.ErrorCodeInvalidCursor
	case errors.Is(err, conversationaudit.ErrTimeRangeRequired):
		return conversationaudit.ErrorCodeDeleteTimeRangeRequired
	case errors.Is(err, conversationaudit.ErrQueryLimit):
		return conversationaudit.ErrorCodeQueryLimitExceeded
	case errors.Is(err, conversationaudit.ErrInvalidDeleteConfirmation):
		return conversationaudit.ErrorCodeDeleteConfirmationInvalid
	default:
		return ""
	}
}

func writeConversationAuditError(c *gin.Context, err error) {
	var appErr error
	switch reason := conversationAuditReason(err); reason {
	case conversationaudit.ErrorCodeInvalidConfig:
		appErr = infraerrors.BadRequest(reason, "会话审计配置无效")
	case conversationaudit.ErrorCodeConfigConflict:
		appErr = infraerrors.Conflict(reason, "会话审计配置已被更新")
	case conversationaudit.ErrorCodeEncryptionUnavailable:
		appErr = infraerrors.ServiceUnavailable(reason, "会话审计加密配置不可用")
	case conversationaudit.ErrorCodeRecordNotFound:
		appErr = infraerrors.NotFound(reason, "会话审计记录不存在")
	case conversationaudit.ErrorCodeRecordBusy:
		appErr = infraerrors.Conflict(reason, "会话审计记录仍在写入或刚完成")
	case conversationaudit.ErrorCodeInvalidCursor:
		appErr = infraerrors.BadRequest(reason, "会话审计分页游标无效")
	case conversationaudit.ErrorCodeDeleteTimeRangeRequired:
		appErr = infraerrors.BadRequest(reason, "删除操作需要有效的时间范围")
	case conversationaudit.ErrorCodeQueryLimitExceeded:
		appErr = infraerrors.BadRequest(reason, "会话审计查询范围或参数超出限制")
	case conversationaudit.ErrorCodeDeleteConfirmationInvalid:
		appErr = infraerrors.BadRequest(reason, "删除确认无效或已过期")
	default:
		appErr = err
	}
	response.ErrorFrom(c, appErr)
}

var _ conversationAuditAdminService = (*conversationaudit.AdminService)(nil)
