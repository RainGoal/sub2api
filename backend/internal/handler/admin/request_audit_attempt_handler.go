package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *RequestAuditHandler) ListAttempts(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"audit_id": id})
	items, err := h.store.ListAttempts(c.Request.Context(), id)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	if items == nil {
		items = []requestaudit.AttemptRecord{}
	}
	response.Success(c, gin.H{"items": items})
}

func (h *RequestAuditHandler) AttemptPayload(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	attempt, err := strconv.Atoi(c.Param("attempt"))
	if err != nil || attempt < 1 {
		requestAuditInvalid(c, "Invalid upstream attempt")
		return
	}
	part := c.Query("part")
	switch part {
	case requestaudit.UpstreamRequestHeaders, requestaudit.UpstreamResponseHeaders, requestaudit.UpstreamRequestBody, requestaudit.UpstreamResponseBody:
	default:
		requestAuditInvalid(c, "Invalid upstream payload part")
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"audit_id": id, "attempt": attempt, "part": part})
	read := func(ctx context.Context, offset, limit int64, partial bool) (requestaudit.PayloadResult, error) {
		return h.store.ReadAttemptPayload(ctx, id, attempt, part, offset, limit, partial)
	}
	h.servePayload(c, id, part, fmt.Sprintf("request-%s-attempt-%d-%s.bin", id, attempt, part), read, false)
}
