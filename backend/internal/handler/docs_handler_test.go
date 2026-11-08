package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type docsPublicTestRepository struct {
	service.DocsRepository
	records []service.DocPublishedRecord
	locale  string
}

func (r *docsPublicTestRepository) Published(_ context.Context, locale, _, _ string, _ bool) ([]service.DocPublishedRecord, error) {
	r.locale = locale
	return r.records, nil
}

func TestDocsPublicResponseContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	published := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	repo := &docsPublicTestRepository{records: []service.DocPublishedRecord{{DocPage: service.DocPage{
		ID: 2, Slug: "quickstart", Title: "快速开始", Summary: "已发布摘要", ContentMD: "![image](data:image/png;base64,AAAA)", Locale: "zh-CN", PublishedAt: published, UpdatedAt: published,
	}}}}
	h := NewDocsHandler(service.NewDocsService(repo))
	router := gin.New()
	router.GET("/docs/pages/:slug", h.Page)
	router.GET("/docs/navigation", h.Navigation)
	router.GET("/docs/search", h.Search)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/pages/quickstart?locale=en-US", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"code":0,"message":"success","data":{"id":2,"slug":"quickstart","title":"快速开始","summary":"已发布摘要","content_md":"![image](data:image/png;base64,AAAA)","locale":"zh-CN","published_at":"2026-11-08T00:00:00Z","updated_at":"2026-11-08T00:00:00Z"}}`, w.Body.String())
	require.Equal(t, "en-US", repo.locale)

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/navigation?locale=unknown", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "zh-CN", repo.locale)
	require.Contains(t, w.Body.String(), `"locale":"zh-CN"`)
	require.NotContains(t, w.Body.String(), "content_md")

	repo.records = nil
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/pages/quickstart", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), `"reason":"DOCS_NOT_FOUND"`)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/search?q=missing", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"items":[]`)
}
