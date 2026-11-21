package admin

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

type requestAuditGroupPolicyStore interface {
	GetGroupPolicies(context.Context) (requestaudit.GroupPolicyConfig, error)
	UpdateGroupPolicies(context.Context, requestaudit.GroupPolicyConfig, int64) (requestaudit.GroupPolicyConfig, error)
}

func (h *RequestAuditHandler) groupPolicyStore(c *gin.Context) requestAuditGroupPolicyStore {
	if !h.authorize(c) {
		return nil
	}
	store, ok := h.store.(requestAuditGroupPolicyStore)
	if !ok {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Request audit group policy storage is unavailable", "AUDIT_STORAGE_UNAVAILABLE", nil)
	}
	return store
}

func (h *RequestAuditHandler) GetGroupPolicies(c *gin.Context) {
	store := h.groupPolicyStore(c)
	if store == nil {
		return
	}
	result, err := store.GetGroupPolicies(c.Request.Context())
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) UpdateGroupPolicies(c *gin.Context) {
	store := h.groupPolicyStore(c)
	if store == nil {
		return
	}
	var input struct {
		GroupOnly       *bool                       `json:"group_only" binding:"required"`
		Rules           *[]requestaudit.GroupPolicy `json:"rules" binding:"required"`
		ExpectedVersion int64                       `json:"expected_version" binding:"min=1"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		requestAuditInvalid(c, "Invalid audit group policies")
		return
	}
	result, err := store.UpdateGroupPolicies(c.Request.Context(), requestaudit.GroupPolicyConfig{
		GroupOnly: *input.GroupOnly, Rules: *input.Rules,
	}, input.ExpectedVersion)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"group_only": result.GroupOnly, "rule_count": len(result.Rules), "config_version": result.Version})
	response.Success(c, result)
}
