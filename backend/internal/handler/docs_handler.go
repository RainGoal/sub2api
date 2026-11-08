package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type DocsHandler struct{ docs *service.DocsService }

func NewDocsHandler(docs *service.DocsService) *DocsHandler { return &DocsHandler{docs: docs} }

func (h *DocsHandler) Navigation(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	items, err := h.docs.Navigation(c.Request.Context(), c.Query("locale"))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *DocsHandler) Page(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	page, err := h.docs.Page(c.Request.Context(), c.Param("slug"), c.Query("locale"))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, page)
}

func (h *DocsHandler) Search(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	items, err := h.docs.Search(c.Request.Context(), c.Query("q"), c.Query("locale"))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"items": items})
}
