package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func videoTaskReviewAdmin(c *gin.Context) (int64, bool) {
	subject, authenticated := middleware.GetAuthSubjectFromContext(c)
	if !authenticated || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return 0, false
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	if role != service.RoleAdmin {
		response.Error(c, http.StatusForbidden, "Administrator access required")
		return 0, false
	}
	return subject.UserID, true
}

// Explicit output fields keep raw provider errors and execution metadata out
// of the administrative review response as well as the public response.
func videoTaskReviewResponse(task *service.SeedanceVideoPendingBilling) gin.H {
	return gin.H{"state_id": task.StateID, "public_id": task.PublicID(), "upstream_id": task.UpstreamID(),
		"user_id": task.UserID, "api_key_id": task.APIKeyID, "account_id": task.AccountID,
		"model": task.Model, "resolution": task.Resolution, "duration": task.DurationSeconds,
		"upstream_status": task.UpstreamStatus, "settlement_status": task.SettlementStatus,
		"reason": task.ReviewReason, "requested_at": task.ReviewRequestedAt, "deadline": task.ReviewDeadline,
		"decisions": task.ReviewDecisions, "sales_snapshot": task.SalesSnapshot, "account_cost": task.AccountCost}
}

func (h *OpenAIGatewayHandler) ListVideoTaskReviews(c *gin.Context) {
	if _, ok := videoTaskReviewAdmin(c); !ok {
		return
	}
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limitErr != nil || offsetErr != nil || limit < 1 || limit > 100 || offset < 0 {
		response.ErrorFrom(c, service.ErrVideoTaskReviewInvalid)
		return
	}
	items, err := h.gatewayService.ListSeedanceVideoTaskReviews(c.Request.Context(), limit, offset)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result := make([]gin.H, 0, len(items))
	for _, item := range items {
		result = append(result, videoTaskReviewResponse(item))
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"items": result, "limit": limit, "offset": offset})
}

func (h *OpenAIGatewayHandler) ResolveVideoTaskReview(c *gin.Context) {
	actorID, ok := videoTaskReviewAdmin(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
		response.ErrorFrom(c, service.ErrVideoTaskReviewInvalid)
		return
	}
	middleware.SetAuditAction(c, "video_task.review.resolve")
	task, err := h.gatewayService.ResolveSeedanceVideoTaskReview(c.Request.Context(), c.Param("state_id"), actorID, request.Action, request.Reason)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, videoTaskReviewResponse(task))
}
