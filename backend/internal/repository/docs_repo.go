package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type docsRepository struct{ db *sql.DB }

func NewDocsRepository(db *sql.DB) service.DocsRepository { return &docsRepository{db: db} }

type docsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func docsNodeSelect(includeContent bool) string {
	content := "''"
	if includeContent {
		content = "c.draft_content_md"
	}
	return `SELECT n.id,n.kind,n.parent_id,n.slug,n.title_zh,n.title_en,n.sort_order,n.revision,n.slug_locked,n.created_at,n.updated_at,
COALESCE((SELECT jsonb_object_agg(c.locale,jsonb_build_object(
'title',c.draft_title,'summary',c.draft_summary,'content_md',` + content + `,
'revision',c.revision,'published_revision',c.published_revision,'published_at',c.published_at))
FROM custom_docs_contents c WHERE c.node_id=n.id),'{}'::jsonb)
FROM custom_docs_nodes n`
}

func docsScanNode(scan func(...any) error) (*service.DocNode, error) {
	var node service.DocNode
	var locales []byte
	if err := scan(&node.ID, &node.Kind, &node.ParentID, &node.Slug, &node.TitleZH, &node.TitleEN, &node.SortOrder, &node.Revision, &node.SlugLocked, &node.CreatedAt, &node.UpdatedAt, &locales); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrDocsNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(locales, &node.Locales); err != nil {
		return nil, err
	}
	return &node, nil
}

func docsGet(ctx context.Context, db docsQueryer, id int64) (*service.DocNode, error) {
	return docsScanNode(db.QueryRowContext(ctx, docsNodeSelect(true)+` WHERE n.id=$1`, id).Scan)
}

func (r *docsRepository) Get(ctx context.Context, id int64) (*service.DocNode, error) {
	return docsGet(ctx, r.db, id)
}

func (r *docsRepository) List(ctx context.Context) ([]service.DocNode, error) {
	rows, err := r.db.QueryContext(ctx, docsNodeSelect(false)+` ORDER BY n.sort_order,n.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.DocNode, 0)
	for rows.Next() {
		node, err := docsScanNode(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, *node)
	}
	return items, rows.Err()
}

// One transaction lock serializes directory changes and publications. This is a
// small CMS, so the lock avoids races between moves, deletes and first publish
// without affecting any gateway or billing tables.
func (r *docsRepository) write(ctx context.Context, action func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(2500963271410)`); err != nil {
		return err
	}
	if err = action(tx); err != nil {
		return docsTranslateError(err)
	}
	return docsTranslateError(tx.Commit())
}

func docsTranslateError(err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) {
		if pg.Code == "23505" {
			return service.ErrDocsSlugTaken
		}
		if pg.Code == "23503" || pg.Code == "23514" {
			return service.ErrDocsInvalid
		}
	}
	return err
}

func docsValidateParent(ctx context.Context, tx *sql.Tx, kind string, parent *int64) error {
	if parent == nil {
		return nil
	}
	if kind != "page" || *parent <= 0 {
		return service.ErrDocsInvalid
	}
	var parentKind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM custom_docs_nodes WHERE id=$1`, *parent).Scan(&parentKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrDocsInvalid
		}
		return err
	}
	if parentKind != "group" {
		return service.ErrDocsInvalid
	}
	return nil
}

func (r *docsRepository) Create(ctx context.Context, in service.CreateDocNodeInput, actor int64) (*service.DocNode, error) {
	var result *service.DocNode
	err := r.write(ctx, func(tx *sql.Tx) error {
		if err := docsValidateParent(ctx, tx, in.Kind, in.ParentID); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRowContext(ctx, `INSERT INTO custom_docs_nodes(kind,parent_id,slug,title_zh,title_en,sort_order,created_by,updated_by)
VALUES($1,$2,$3,$4,$5,(SELECT COALESCE(MAX(sort_order),-1)+1 FROM custom_docs_nodes WHERE parent_id IS NOT DISTINCT FROM $2::bigint),$6,$6) RETURNING id`, in.Kind, in.ParentID, in.Slug, in.TitleZH, in.TitleEN, actor).Scan(&id)
		if err != nil {
			return err
		}
		result, err = docsGet(ctx, tx, id)
		return err
	})
	return result, err
}

func (r *docsRepository) Update(ctx context.Context, id int64, in service.UpdateDocNodeInput, actor int64) (*service.DocNode, error) {
	var result *service.DocNode
	err := r.write(ctx, func(tx *sql.Tx) error {
		node, err := docsGet(ctx, tx, id)
		if err != nil {
			return err
		}
		if in.TitleZH != nil {
			node.TitleZH = *in.TitleZH
		}
		if in.TitleEN != nil {
			node.TitleEN = *in.TitleEN
		}
		if in.Slug != nil && *in.Slug != node.Slug {
			if node.SlugLocked {
				return service.ErrDocsSlugLocked
			}
			node.Slug = *in.Slug
		}
		if in.ParentSet {
			node.ParentID = in.ParentID
		}
		if err := docsValidateParent(ctx, tx, node.Kind, node.ParentID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE custom_docs_nodes SET title_zh=$2,title_en=$3,slug=$4,parent_id=$5,updated_by=$6,updated_at=NOW(),revision=revision+1 WHERE id=$1`, id, node.TitleZH, node.TitleEN, node.Slug, node.ParentID, actor)
		if err != nil {
			return err
		}
		result, err = docsGet(ctx, tx, id)
		return err
	})
	return result, err
}

func (r *docsRepository) Delete(ctx context.Context, id int64) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if _, err := docsGet(ctx, tx, id); err != nil {
			return err
		}
		var blocked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_docs_nodes WHERE parent_id=$1)`, id).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return service.ErrDocsNotEmpty
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_docs_contents WHERE node_id=$1 AND published_revision IS NOT NULL)`, id).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return service.ErrDocsPublished
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM custom_docs_nodes WHERE id=$1`, id)
		return err
	})
}

func (r *docsRepository) SaveTree(ctx context.Context, items []service.DocTreeItem, actor int64) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,kind FROM custom_docs_nodes`)
		if err != nil {
			return err
		}
		kinds := make(map[int64]string)
		for rows.Next() {
			var id int64
			var kind string
			if err = rows.Scan(&id, &kind); err != nil {
				_ = rows.Close()
				return err
			}
			kinds[id] = kind
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if len(kinds) != len(items) {
			return service.ErrDocsConflict
		}
		for _, item := range items {
			kind, exists := kinds[item.ID]
			if !exists {
				return service.ErrDocsConflict
			}
			if item.ParentID != nil && (kind != "page" || kinds[*item.ParentID] != "group") {
				return service.ErrDocsInvalid
			}
		}
		for _, item := range items {
			_, err = tx.ExecContext(ctx, `UPDATE custom_docs_nodes SET parent_id=$2,sort_order=$3,revision=revision+1,updated_at=NOW(),updated_by=$4 WHERE id=$1`, item.ID, item.ParentID, item.SortOrder, actor)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *docsRepository) SaveDraft(ctx context.Context, id int64, in service.DocDraftInput, actor int64) (*service.DocNode, error) {
	var result *service.DocNode
	err := r.write(ctx, func(tx *sql.Tx) error {
		node, err := docsGet(ctx, tx, id)
		if err != nil {
			return err
		}
		if node.Kind != "page" {
			return service.ErrDocsInvalid
		}
		current, exists := node.Locales[in.Locale]
		if (!exists && in.Revision != 0) || (exists && current.Revision != in.Revision) {
			return service.ErrDocsConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO custom_docs_contents(node_id,locale,draft_title,draft_summary,draft_content_md,updated_by)
VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(node_id,locale) DO UPDATE SET draft_title=EXCLUDED.draft_title,draft_summary=EXCLUDED.draft_summary,draft_content_md=EXCLUDED.draft_content_md,revision=custom_docs_contents.revision+1,updated_by=EXCLUDED.updated_by,updated_at=NOW()`, id, in.Locale, in.Title, in.Summary, in.ContentMD, actor)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE custom_docs_nodes SET updated_at=NOW(),updated_by=$2,revision=revision+1 WHERE id=$1`, id, actor)
		if err != nil {
			return err
		}
		result, err = docsGet(ctx, tx, id)
		return err
	})
	return result, err
}

func (r *docsRepository) Publish(ctx context.Context, id int64, in service.DocPublishInput, actor int64, publish bool) (*service.DocNode, error) {
	var result *service.DocNode
	err := r.write(ctx, func(tx *sql.Tx) error {
		node, err := docsGet(ctx, tx, id)
		if err != nil {
			return err
		}
		if node.Kind != "page" {
			return service.ErrDocsInvalid
		}
		content, exists := node.Locales[in.Locale]
		if !exists || content.Revision != in.Revision {
			return service.ErrDocsConflict
		}
		if publish {
			if strings.TrimSpace(content.ContentMD) == "" {
				return service.ErrDocsInvalid
			}
			_, err = tx.ExecContext(ctx, `UPDATE custom_docs_contents SET published_title=draft_title,published_summary=draft_summary,published_content_md=draft_content_md,published_search_text=$3,published_revision=revision+1,revision=revision+1,published_at=NOW(),published_by=$4,updated_at=NOW(),updated_by=$4 WHERE node_id=$1 AND locale=$2`, id, in.Locale, service.DocsSearchText(content.ContentMD), actor)
		} else {
			if content.PublishedRevision == nil {
				return service.ErrDocsConflict
			}
			_, err = tx.ExecContext(ctx, `UPDATE custom_docs_contents SET published_title=NULL,published_summary=NULL,published_content_md=NULL,published_search_text=NULL,published_revision=NULL,published_at=NULL,published_by=NULL,revision=revision+1,updated_at=NOW(),updated_by=$3 WHERE node_id=$1 AND locale=$2`, id, in.Locale, actor)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE custom_docs_nodes SET slug_locked=(slug_locked OR $3),updated_at=NOW(),updated_by=$2,revision=revision+1 WHERE id=$1`, id, actor, publish)
		if err != nil {
			return err
		}
		result, err = docsGet(ctx, tx, id)
		return err
	})
	return result, err
}

func (r *docsRepository) Published(ctx context.Context, locale, slug, search string, includeContent bool) ([]service.DocPublishedRecord, error) {
	content := "''"
	if includeContent {
		content = "c.published_content_md"
	}
	// Select a language first, then apply search, so an English publication never
	// unexpectedly searches a hidden Chinese alternative. Draft columns are absent.
	query := `SELECT n.id,n.slug,c.published_title,c.published_summary,` + content + `,c.locale,c.published_at,c.published_at,
n.parent_id,n.sort_order,COALESCE(g.title_zh,''),COALESCE(g.title_en,''),COALESCE(g.sort_order,0),COALESCE(g.slug,'')
FROM custom_docs_nodes n
LEFT JOIN custom_docs_nodes g ON g.id=n.parent_id
JOIN LATERAL (SELECT locale,published_title,published_summary,published_content_md,published_search_text,published_at
FROM custom_docs_contents WHERE node_id=n.id AND published_revision IS NOT NULL AND locale IN ($1,'zh-CN')
ORDER BY CASE WHEN locale=$1 THEN 0 ELSE 1 END LIMIT 1) c ON TRUE
WHERE n.kind='page' AND ($2='' OR n.slug=$2)
AND ($3='' OR POSITION(LOWER($3) IN LOWER(c.published_title || ' ' || c.published_summary || ' ' || c.published_search_text))>0)
ORDER BY n.sort_order,n.id LIMIT $4`
	limit := int64(2147483647)
	if search != "" {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, query, locale, slug, search, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.DocPublishedRecord, 0)
	for rows.Next() {
		var item service.DocPublishedRecord
		if err = rows.Scan(&item.ID, &item.Slug, &item.Title, &item.Summary, &item.ContentMD, &item.Locale, &item.PublishedAt, &item.UpdatedAt, &item.ParentID, &item.SortOrder, &item.GroupTitleZH, &item.GroupTitleEN, &item.GroupSort, &item.GroupSlug); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
