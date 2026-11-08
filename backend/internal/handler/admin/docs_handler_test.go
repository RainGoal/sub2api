package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type docsAdminTestRepository struct {
	service.DocsRepository
	actor  int64
	draft  service.DocDraftInput
	update service.UpdateDocNodeInput
	err    error
}

func (r *docsAdminTestRepository) SaveDraft(_ context.Context, id int64, in service.DocDraftInput, actor int64) (*service.DocNode, error) {
	r.actor, r.draft = actor, in
	return &service.DocNode{ID: id}, r.err
}

func (r *docsAdminTestRepository) Update(_ context.Context, id int64, in service.UpdateDocNodeInput, actor int64) (*service.DocNode, error) {
	r.actor, r.update = actor, in
	return &service.DocNode{ID: id}, r.err
}

func docsAdminFixture(auth bool) (*gin.Engine, *docsAdminTestRepository) {
	gin.SetMode(gin.TestMode)
	repo := &docsAdminTestRepository{}
	h := NewDocsHandler(service.NewDocsService(repo))
	router := gin.New()
	if auth {
		router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42}) })
	}
	router.PUT("/docs/nodes/:id", h.Update)
	router.PUT("/docs/pages/:id/draft", h.SaveDraft)
	return router, repo
}

func docsAdminRequest(router *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestDocsAdminDraftAuthValidationAndConflict(t *testing.T) {
	body := `{"locale":"zh-CN","title":"快速开始","content_md":"![image](data:image/png;base64,AAAA)","revision":3}`
	router, _ := docsAdminFixture(false)
	require.Equal(t, http.StatusUnauthorized, docsAdminRequest(router, "/docs/pages/2/draft", body).Code)
	router, repo := docsAdminFixture(true)
	require.Equal(t, http.StatusBadRequest, docsAdminRequest(router, "/docs/pages/invalid/draft", body).Code)
	require.Equal(t, http.StatusBadRequest, docsAdminRequest(router, "/docs/pages/2/draft", `{`).Code)
	w := docsAdminRequest(router, "/docs/pages/2/draft", body)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(42), repo.actor)
	require.Equal(t, int64(3), repo.draft.Revision)
	require.Contains(t, repo.draft.ContentMD, "data:image/png;base64,AAAA")

	repo.err = service.ErrDocsConflict
	w = docsAdminRequest(router, "/docs/pages/2/draft", body)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), `"reason":"DOCS_CONFLICT"`)
}

func TestDocsAdminParentOmittedVersusNull(t *testing.T) {
	router, repo := docsAdminFixture(true)
	require.Equal(t, http.StatusOK, docsAdminRequest(router, "/docs/nodes/2", `{"title_zh":"新名称"}`).Code)
	require.False(t, repo.update.ParentSet)
	require.Equal(t, http.StatusOK, docsAdminRequest(router, "/docs/nodes/2", `{"parent_id":null}`).Code)
	require.True(t, repo.update.ParentSet)
	require.Nil(t, repo.update.ParentID)
	require.Equal(t, http.StatusOK, docsAdminRequest(router, "/docs/nodes/2", `{"parent_id":9}`).Code)
	require.NotNil(t, repo.update.ParentID)
	require.Equal(t, int64(9), *repo.update.ParentID)
	require.Equal(t, http.StatusBadRequest, docsAdminRequest(router, "/docs/nodes/2", `{"parent_id":"invalid"}`).Code)
}

func TestDocsAdminRejectsLargeBodyAndDecodedContent(t *testing.T) {
	router, _ := docsAdminFixture(true)
	body, err := json.Marshal(service.DocDraftInput{Locale: "zh-CN", Title: "Title", ContentMD: strings.Repeat("a", service.DocsMaxContentBytes+1)})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, docsAdminRequest(router, "/docs/pages/2/draft", string(body)).Code)
	body, err = json.Marshal(service.DocDraftInput{Locale: "zh-CN", Title: "Title", ContentMD: strings.Repeat("a", 2*service.DocsMaxContentBytes+65536)})
	require.NoError(t, err)
	require.Equal(t, http.StatusRequestEntityTooLarge, docsAdminRequest(router, "/docs/pages/2/draft", string(body)).Code)
}
