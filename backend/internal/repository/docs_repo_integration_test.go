//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDocsPublicationLifecycleAndChineseFallback(t *testing.T) {
	ctx := context.Background()
	repo := NewDocsRepository(integrationDB)
	svc := service.NewDocsService(repo)
	group, err := svc.Create(ctx, service.CreateDocNodeInput{Kind: "group", Slug: fmt.Sprintf("docs-group-%d", time.Now().UnixNano()), TitleZH: "接入", TitleEN: "Integration"}, 1)
	require.NoError(t, err)
	page, err := svc.Create(ctx, service.CreateDocNodeInput{Kind: "page", ParentID: &group.ID, Slug: fmt.Sprintf("docs-page-%d", time.Now().UnixNano()), TitleZH: "初始名称"}, 1)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(ctx, `DELETE FROM custom_docs_nodes WHERE id=$1`, page.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, `DELETE FROM custom_docs_nodes WHERE id=$1`, group.ID)
		require.NoError(t, cleanupErr)
	})
	_, err = svc.Page(ctx, page.Slug, "en-US")
	require.ErrorIs(t, err, service.ErrDocsNotFound)
	page, err = svc.SaveDraft(ctx, page.ID, service.DocDraftInput{Locale: "zh-CN", Title: "中文教程", ContentMD: "Initial published text\n![Image](data:image/png;base64,U0VDUkVU)", Revision: 0}, 1)
	require.NoError(t, err)
	page, err = svc.Publish(ctx, page.ID, service.DocPublishInput{Locale: "zh-CN", Revision: page.Locales["zh-CN"].Revision}, 1, true)
	require.NoError(t, err)
	published, err := svc.Page(ctx, page.Slug, "en-US")
	require.NoError(t, err)
	require.Equal(t, "zh-CN", published.Locale)
	require.Equal(t, "中文教程", published.Title)
	oldRevision := page.Locales["zh-CN"].Revision
	page, err = svc.SaveDraft(ctx, page.ID, service.DocDraftInput{Locale: "zh-CN", Title: "尚未发布", ContentMD: "PRIVATE-UNPUBLISHED-CHANGE", Revision: oldRevision}, 1)
	require.NoError(t, err)
	published, err = svc.Page(ctx, page.Slug, "zh-CN")
	require.NoError(t, err)
	require.Contains(t, published.ContentMD, "Initial published text")
	require.Equal(t, "中文教程", published.Title)
	results, err := svc.Search(ctx, "PRIVATE-UNPUBLISHED-CHANGE", "en-US")
	require.NoError(t, err)
	require.Empty(t, results)
	results, err = svc.Search(ctx, "U0VDUkVU", "zh-CN")
	require.NoError(t, err)
	require.Empty(t, results)
	_, err = svc.Publish(ctx, page.ID, service.DocPublishInput{Locale: "zh-CN", Revision: oldRevision}, 1, true)
	require.ErrorIs(t, err, service.ErrDocsConflict)
	page, err = svc.SaveDraft(ctx, page.ID, service.DocDraftInput{Locale: "en-US", Title: "English guide", ContentMD: "English published body", Revision: 0}, 1)
	require.NoError(t, err)
	page, err = svc.Publish(ctx, page.ID, service.DocPublishInput{Locale: "en-US", Revision: page.Locales["en-US"].Revision}, 1, true)
	require.NoError(t, err)
	published, err = svc.Page(ctx, page.Slug, "en-US")
	require.NoError(t, err)
	require.Equal(t, "en-US", published.Locale)
	require.Equal(t, "English guide", published.Title)
	_, err = svc.Update(ctx, page.ID, service.UpdateDocNodeInput{Slug: func() *string { s := "changed-url"; return &s }()}, 1)
	require.ErrorIs(t, err, service.ErrDocsSlugLocked)
	require.ErrorIs(t, svc.Delete(ctx, page.ID), service.ErrDocsPublished)
	require.ErrorIs(t, svc.Delete(ctx, group.ID), service.ErrDocsNotEmpty)
	page, err = svc.Publish(ctx, page.ID, service.DocPublishInput{Locale: "en-US", Revision: page.Locales["en-US"].Revision}, 1, false)
	require.NoError(t, err)
	published, err = svc.Page(ctx, page.Slug, "en-US")
	require.NoError(t, err)
	require.Equal(t, "zh-CN", published.Locale)
	page, err = svc.Publish(ctx, page.ID, service.DocPublishInput{Locale: "zh-CN", Revision: page.Locales["zh-CN"].Revision}, 1, false)
	require.NoError(t, err)
	_, err = svc.Page(ctx, page.Slug, "en-US")
	require.ErrorIs(t, err, service.ErrDocsNotFound)
	results, err = svc.Search(ctx, "Initial published text", "zh-CN")
	require.NoError(t, err)
	require.Empty(t, results)
	require.NoError(t, svc.Delete(ctx, page.ID))
	require.NoError(t, svc.Delete(ctx, group.ID))
}
