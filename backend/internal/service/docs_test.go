package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type docsCaptureRepository struct {
	DocsRepository
	created              CreateDocNodeInput
	draft                DocDraftInput
	records              []DocPublishedRecord
	locale, slug, search string
}

func (r *docsCaptureRepository) Create(_ context.Context, in CreateDocNodeInput, _ int64) (*DocNode, error) {
	r.created = in
	return &DocNode{Slug: in.Slug}, nil
}
func (r *docsCaptureRepository) SaveDraft(_ context.Context, _ int64, in DocDraftInput, _ int64) (*DocNode, error) {
	r.draft = in
	return &DocNode{}, nil
}
func (r *docsCaptureRepository) Published(_ context.Context, locale, slug, search string, _ bool) ([]DocPublishedRecord, error) {
	r.locale, r.slug, r.search = locale, slug, search
	return r.records, nil
}

func TestDocsRejectsInvalidPathsAndContent(t *testing.T) {
	repo := &docsCaptureRepository{}
	svc := NewDocsService(repo)
	for _, slug := range []string{"../private", "read/me", "HELLO", "", "-intro", "intro-", strings.Repeat("a", 97)} {
		_, err := svc.Create(context.Background(), CreateDocNodeInput{Kind: "page", Slug: slug, TitleZH: "指南"}, 1)
		require.ErrorIs(t, err, ErrDocsInvalid, slug)
	}
	_, err := svc.Create(context.Background(), CreateDocNodeInput{Kind: "page", Slug: " quick-start ", TitleZH: " 快速开始 "}, 1)
	require.NoError(t, err)
	require.Equal(t, "quick-start", repo.created.Slug)
	require.Equal(t, "快速开始", repo.created.TitleZH)
	for _, in := range []DocDraftInput{
		{Locale: "zh-CN", Title: "Guide", ContentMD: strings.Repeat("a", DocsMaxContentBytes+1)},
		{Locale: "fr", Title: "Guide"},
		{Locale: "en-US", Title: "", Revision: 1},
		{Locale: "en-US", Title: "Guide", Revision: -1},
	} {
		_, err = svc.SaveDraft(context.Background(), 1, in, 1)
		require.ErrorIs(t, err, ErrDocsInvalid)
	}
	image := "![Screenshot](data:image/png;base64,aGVsbG8=)"
	_, err = svc.SaveDraft(context.Background(), 1, DocDraftInput{Locale: "zh-CN", Title: "Guide", ContentMD: image}, 1)
	require.NoError(t, err)
	require.Equal(t, image, repo.draft.ContentMD)
}

func TestDocsSearchTextExcludesBase64AndHTML(t *testing.T) {
	text := DocsSearchText("## Topic\n\n![Screenshot](data:image/png;base64,SECRETENCODING)\n<p>Visible paragraph</p>\n<img src=\"data:image/webp;base64,ANOTHERSECRET\" />")
	require.Contains(t, text, "Topic")
	require.Contains(t, text, "Visible paragraph")
	require.NotContains(t, text, "SECRET")
	require.NotContains(t, text, "<p>")
}

func TestDocsNavigationUsesResolvedPublicationAndHidesEmptyGroups(t *testing.T) {
	parent := int64(8)
	repo := &docsCaptureRepository{records: []DocPublishedRecord{
		{DocPage: DocPage{ID: 2, Slug: "codex", Title: "中文教程", Locale: "zh-CN"}, ParentID: &parent, SortOrder: 0, GroupTitleZH: "客户端", GroupTitleEN: "Clients", GroupSort: 2, GroupSlug: "clients"},
		{DocPage: DocPage{ID: 3, Slug: "quick-start", Title: "Start", Locale: "en-US"}, SortOrder: 0},
	}}
	items, err := NewDocsService(repo).Navigation(context.Background(), "en-US")
	require.NoError(t, err)
	require.Equal(t, "en-US", repo.locale)
	require.Len(t, items, 2)
	require.Equal(t, "quick-start", items[0].Slug)
	require.Equal(t, "Clients", items[1].Title)
	require.Len(t, items[1].Children, 1)
	require.Equal(t, "中文教程", items[1].Children[0].Title)
	require.Equal(t, "zh-CN", items[1].Children[0].Locale)
	_, err = NewDocsService(repo).Search(context.Background(), strings.Repeat("查", 201), "en-US")
	require.ErrorIs(t, err, ErrDocsInvalid)
}

func TestDocsTreeRejectsDuplicateAndSelfReferencingNodes(t *testing.T) {
	svc := NewDocsService(&docsCaptureRepository{})
	parent := int64(1)
	for _, items := range [][]DocTreeItem{
		{{ID: 1}, {ID: 1}}, {{ID: 1, ParentID: &parent}}, {{ID: 1, SortOrder: -1}},
	} {
		require.ErrorIs(t, svc.SaveTree(context.Background(), items, 1), ErrDocsInvalid)
	}
}
