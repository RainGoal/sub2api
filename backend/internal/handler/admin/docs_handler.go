package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type DocsHandler struct{ docs *service.DocsService }

func NewDocsHandler(docs *service.DocsService) *DocsHandler { return &DocsHandler{docs: docs} }

func docsID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid document ID")
		return 0, false
	}
	return id, true
}

func docsActor(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Administrator authentication required")
		return 0, false
	}
	return subject.UserID, true
}

func docsBind(c *gin.Context, value any) bool {
	// JSON escaping can double the Markdown size; the service separately checks
	// the decoded 12 MiB limit. Embedded Base64 raster images are ordinary content.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*service.DocsMaxContentBytes+65536)
	if err := c.ShouldBindJSON(value); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.Error(c, http.StatusRequestEntityTooLarge, "Document request is too large")
		} else {
			response.BadRequest(c, "Invalid documentation request")
		}
		return false
	}
	return true
}

func (h *DocsHandler) Tree(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	items, err := h.docs.List(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *DocsHandler) Get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, ok := docsID(c)
	if !ok {
		return
	}
	node, err := h.docs.Get(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, node)
}

func (h *DocsHandler) Create(c *gin.Context) {
	actor, ok := docsActor(c)
	if !ok {
		return
	}
	var in service.CreateDocNodeInput
	if !docsBind(c, &in) {
		return
	}
	node, err := h.docs.Create(c.Request.Context(), in, actor)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, node)
}

func (h *DocsHandler) Update(c *gin.Context) {
	actor, ok := docsActor(c)
	if !ok {
		return
	}
	id, ok := docsID(c)
	if !ok {
		return
	}
	var body struct {
		TitleZH *string         `json:"title_zh"`
		TitleEN *string         `json:"title_en"`
		Slug    *string         `json:"slug"`
		Parent  json.RawMessage `json:"parent_id"`
	}
	if !docsBind(c, &body) {
		return
	}
	in := service.UpdateDocNodeInput{TitleZH: body.TitleZH, TitleEN: body.TitleEN, Slug: body.Slug, ParentSet: body.Parent != nil}
	if body.Parent != nil {
		if err := json.Unmarshal(body.Parent, &in.ParentID); err != nil {
			response.BadRequest(c, "Invalid parent document ID")
			return
		}
	}
	node, err := h.docs.Update(c.Request.Context(), id, in, actor)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, node)
}

func (h *DocsHandler) Delete(c *gin.Context) {
	if _, ok := docsActor(c); !ok {
		return
	}
	id, ok := docsID(c)
	if !ok {
		return
	}
	if response.ErrorFrom(c, h.docs.Delete(c.Request.Context(), id)) {
		return
	}
	response.Success(c, gin.H{})
}

func (h *DocsHandler) SaveTree(c *gin.Context) {
	actor, ok := docsActor(c)
	if !ok {
		return
	}
	var body struct {
		Items []service.DocTreeItem `json:"items"`
	}
	if !docsBind(c, &body) {
		return
	}
	if response.ErrorFrom(c, h.docs.SaveTree(c.Request.Context(), body.Items, actor)) {
		return
	}
	response.Success(c, gin.H{})
}

func (h *DocsHandler) writePage(c *gin.Context, action func(context.Context, int64, int64) (*service.DocNode, error)) {
	actor, ok := docsActor(c)
	if !ok {
		return
	}
	id, ok := docsID(c)
	if !ok {
		return
	}
	node, err := action(c.Request.Context(), id, actor)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, node)
}

func (h *DocsHandler) SaveDraft(c *gin.Context) {
	var in service.DocDraftInput
	if !docsBind(c, &in) {
		return
	}
	h.writePage(c, func(ctx context.Context, id, actor int64) (*service.DocNode, error) {
		return h.docs.SaveDraft(ctx, id, in, actor)
	})
}

func (h *DocsHandler) Publish(c *gin.Context)   { h.publish(c, true) }
func (h *DocsHandler) Unpublish(c *gin.Context) { h.publish(c, false) }

func (h *DocsHandler) publish(c *gin.Context, publish bool) {
	var in service.DocPublishInput
	if !docsBind(c, &in) {
		return
	}
	h.writePage(c, func(ctx context.Context, id, actor int64) (*service.DocNode, error) {
		return h.docs.Publish(ctx, id, in, actor, publish)
	})
}
