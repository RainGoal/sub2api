package service

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const DocsMaxContentBytes = 12 << 20

var (
	ErrDocsNotFound   = infraerrors.New(http.StatusNotFound, "DOCS_NOT_FOUND", "Document not found")
	ErrDocsInvalid    = infraerrors.New(http.StatusBadRequest, "DOCS_INVALID", "Invalid document or directory")
	ErrDocsConflict   = infraerrors.New(http.StatusConflict, "DOCS_CONFLICT", "Document changed. Reload before saving or publishing")
	ErrDocsSlugTaken  = infraerrors.New(http.StatusConflict, "DOCS_SLUG_TAKEN", "This document path is already in use")
	ErrDocsSlugLocked = infraerrors.New(http.StatusConflict, "DOCS_SLUG_LOCKED", "A published document path cannot be changed")
	ErrDocsPublished  = infraerrors.New(http.StatusConflict, "DOCS_PUBLISHED", "Unpublish all languages before deleting this document")
	ErrDocsNotEmpty   = infraerrors.New(http.StatusConflict, "DOCS_NOT_EMPTY", "Move or delete the articles before deleting this group")
	docsSlug          = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	docsImages        = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	docsDataURLs      = regexp.MustCompile(`(?i)data:image/[^\s"'<>)]*`)
	docsTags          = regexp.MustCompile(`<[^>]*>`)
)

type DocLocale struct {
	Title             string     `json:"title"`
	Summary           string     `json:"summary"`
	ContentMD         string     `json:"content_md"`
	Revision          int64      `json:"revision"`
	PublishedRevision *int64     `json:"published_revision"`
	PublishedAt       *time.Time `json:"published_at"`
}

type DocNode struct {
	ID         int64                `json:"id"`
	Kind       string               `json:"kind"`
	ParentID   *int64               `json:"parent_id"`
	Slug       string               `json:"slug"`
	TitleZH    string               `json:"title_zh"`
	TitleEN    string               `json:"title_en"`
	SortOrder  int                  `json:"sort_order"`
	Revision   int64                `json:"revision"`
	SlugLocked bool                 `json:"slug_locked"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at"`
	Locales    map[string]DocLocale `json:"locales"`
}

type DocPage struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	ContentMD   string    `json:"content_md"`
	Locale      string    `json:"locale"`
	PublishedAt time.Time `json:"published_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DocPublishedRecord struct {
	DocPage
	ParentID     *int64
	SortOrder    int
	GroupTitleZH string
	GroupTitleEN string
	GroupSort    int
	GroupSlug    string
}

type DocNavNode struct {
	ID       int64        `json:"id"`
	Kind     string       `json:"kind"`
	Title    string       `json:"title"`
	Slug     string       `json:"slug"`
	Locale   string       `json:"locale"`
	Children []DocNavNode `json:"children"`
}

type CreateDocNodeInput struct {
	Kind     string `json:"kind"`
	ParentID *int64 `json:"parent_id"`
	Slug     string `json:"slug"`
	TitleZH  string `json:"title_zh"`
	TitleEN  string `json:"title_en"`
}

type UpdateDocNodeInput struct {
	TitleZH   *string `json:"title_zh"`
	TitleEN   *string `json:"title_en"`
	Slug      *string `json:"slug"`
	ParentID  *int64  `json:"-"`
	ParentSet bool    `json:"-"`
}

type DocTreeItem struct {
	ID        int64  `json:"id"`
	ParentID  *int64 `json:"parent_id"`
	SortOrder int    `json:"sort_order"`
}

type DocDraftInput struct {
	Locale    string `json:"locale"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	ContentMD string `json:"content_md"`
	Revision  int64  `json:"revision"`
}

type DocPublishInput struct {
	Locale   string `json:"locale"`
	Revision int64  `json:"revision"`
}

type DocsRepository interface {
	List(context.Context) ([]DocNode, error)
	Get(context.Context, int64) (*DocNode, error)
	Create(context.Context, CreateDocNodeInput, int64) (*DocNode, error)
	Update(context.Context, int64, UpdateDocNodeInput, int64) (*DocNode, error)
	Delete(context.Context, int64) error
	SaveTree(context.Context, []DocTreeItem, int64) error
	SaveDraft(context.Context, int64, DocDraftInput, int64) (*DocNode, error)
	Publish(context.Context, int64, DocPublishInput, int64, bool) (*DocNode, error)
	Published(context.Context, string, string, string, bool) ([]DocPublishedRecord, error)
}

type DocsService struct{ repo DocsRepository }

func NewDocsService(repo DocsRepository) *DocsService { return &DocsService{repo: repo} }

func DocsLocale(locale string) string {
	if locale == "en-US" {
		return locale
	}
	return "zh-CN"
}

func validDocsLocale(locale string) bool { return locale == "zh-CN" || locale == "en-US" }
func validDocsTitle(title string) bool {
	return strings.TrimSpace(title) != "" && utf8.RuneCountInString(title) <= 200
}

func (s *DocsService) List(ctx context.Context) ([]DocNode, error) { return s.repo.List(ctx) }
func (s *DocsService) Get(ctx context.Context, id int64) (*DocNode, error) {
	return s.repo.Get(ctx, id)
}
func (s *DocsService) Delete(ctx context.Context, id int64) error { return s.repo.Delete(ctx, id) }

func (s *DocsService) Create(ctx context.Context, in CreateDocNodeInput, actor int64) (*DocNode, error) {
	in.TitleZH, in.TitleEN, in.Slug = strings.TrimSpace(in.TitleZH), strings.TrimSpace(in.TitleEN), strings.TrimSpace(in.Slug)
	if (in.Kind != "group" && in.Kind != "page") || !validDocsTitle(in.TitleZH) || utf8.RuneCountInString(in.TitleEN) > 200 || len(in.Slug) > 96 || !docsSlug.MatchString(in.Slug) || (in.Kind == "group" && in.ParentID != nil) {
		return nil, ErrDocsInvalid
	}
	return s.repo.Create(ctx, in, actor)
}

func (s *DocsService) Update(ctx context.Context, id int64, in UpdateDocNodeInput, actor int64) (*DocNode, error) {
	if in.TitleZH != nil {
		v := strings.TrimSpace(*in.TitleZH)
		in.TitleZH = &v
		if !validDocsTitle(v) {
			return nil, ErrDocsInvalid
		}
	}
	if in.TitleEN != nil {
		v := strings.TrimSpace(*in.TitleEN)
		in.TitleEN = &v
		if utf8.RuneCountInString(v) > 200 {
			return nil, ErrDocsInvalid
		}
	}
	if in.Slug != nil {
		v := strings.TrimSpace(*in.Slug)
		in.Slug = &v
		if len(v) > 96 || !docsSlug.MatchString(v) {
			return nil, ErrDocsInvalid
		}
	}
	return s.repo.Update(ctx, id, in, actor)
}

func (s *DocsService) SaveTree(ctx context.Context, items []DocTreeItem, actor int64) error {
	seen := make(map[int64]bool, len(items))
	for _, item := range items {
		if item.ID <= 0 || seen[item.ID] || item.SortOrder < 0 || item.SortOrder > 1000000 || (item.ParentID != nil && (*item.ParentID <= 0 || *item.ParentID == item.ID)) {
			return ErrDocsInvalid
		}
		seen[item.ID] = true
	}
	return s.repo.SaveTree(ctx, items, actor)
}

func (s *DocsService) SaveDraft(ctx context.Context, id int64, in DocDraftInput, actor int64) (*DocNode, error) {
	in.Title, in.Summary = strings.TrimSpace(in.Title), strings.TrimSpace(in.Summary)
	if !validDocsLocale(in.Locale) || !validDocsTitle(in.Title) || utf8.RuneCountInString(in.Summary) > 1000 || len(in.ContentMD) > DocsMaxContentBytes || in.Revision < 0 {
		return nil, ErrDocsInvalid
	}
	return s.repo.SaveDraft(ctx, id, in, actor)
}

func (s *DocsService) Publish(ctx context.Context, id int64, in DocPublishInput, actor int64, publish bool) (*DocNode, error) {
	if !validDocsLocale(in.Locale) || in.Revision <= 0 {
		return nil, ErrDocsInvalid
	}
	return s.repo.Publish(ctx, id, in, actor, publish)
}

// DocsSearchText excludes embedded Base64 images from the searchable text.
func DocsSearchText(content string) string {
	content = docsImages.ReplaceAllString(content, " ")
	content = docsDataURLs.ReplaceAllString(content, " ")
	content = docsTags.ReplaceAllString(content, " ")
	return strings.Join(strings.Fields(content), " ")
}

func (s *DocsService) Page(ctx context.Context, slug, locale string) (*DocPage, error) {
	if len(slug) > 96 || !docsSlug.MatchString(slug) {
		return nil, ErrDocsNotFound
	}
	records, err := s.repo.Published(ctx, DocsLocale(locale), slug, "", true)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrDocsNotFound
	}
	return &records[0].DocPage, nil
}

func (s *DocsService) Search(ctx context.Context, query, locale string) ([]DocPage, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > 200 {
		return nil, ErrDocsInvalid
	}
	result := make([]DocPage, 0)
	if query == "" {
		return result, nil
	}
	records, err := s.repo.Published(ctx, DocsLocale(locale), "", query, false)
	if err != nil {
		return nil, err
	}
	for _, item := range records {
		result = append(result, item.DocPage)
	}
	return result, nil
}

func (s *DocsService) Navigation(ctx context.Context, locale string) ([]DocNavNode, error) {
	locale = DocsLocale(locale)
	records, err := s.repo.Published(ctx, locale, "", "", false)
	if err != nil {
		return nil, err
	}
	result := make([]DocNavNode, 0)
	groupIndices := make(map[int64]int)
	orders := make(map[int64]int)
	for _, item := range records {
		page := DocNavNode{ID: item.ID, Kind: "page", Title: item.Title, Slug: item.Slug, Locale: item.Locale, Children: []DocNavNode{}}
		if item.ParentID == nil {
			result = append(result, page)
			orders[item.ID] = item.SortOrder
			continue
		}
		index, exists := groupIndices[*item.ParentID]
		if !exists {
			title, groupLocale := item.GroupTitleZH, "zh-CN"
			if locale == "en-US" && item.GroupTitleEN != "" {
				title, groupLocale = item.GroupTitleEN, "en-US"
			}
			index = len(result)
			result = append(result, DocNavNode{ID: *item.ParentID, Kind: "group", Title: title, Slug: item.GroupSlug, Locale: groupLocale, Children: []DocNavNode{}})
			groupIndices[*item.ParentID] = index
			orders[*item.ParentID] = item.GroupSort
		}
		result[index].Children = append(result[index].Children, page)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if orders[result[i].ID] == orders[result[j].ID] {
			return result[i].ID < result[j].ID
		}
		return orders[result[i].ID] < orders[result[j].ID]
	})
	return result, nil
}
