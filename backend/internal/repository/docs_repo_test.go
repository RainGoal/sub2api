package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func docsTestNodeRows(t *testing.T, revision int64) *sqlmock.Rows {
	t.Helper()
	publishedRevision := int64(2)
	publishedAt := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	locales, err := json.Marshal(map[string]service.DocLocale{"zh-CN": {
		Title: "Title", ContentMD: "Body text\n![image](data:image/png;base64,AAAA)", Revision: revision, PublishedRevision: &publishedRevision, PublishedAt: &publishedAt,
	}})
	require.NoError(t, err)
	return sqlmock.NewRows([]string{"id", "kind", "parent_id", "slug", "title_zh", "title_en", "sort_order", "revision", "slug_locked", "created_at", "updated_at", "locales"}).
		AddRow(int64(2), "page", nil, "quickstart", "Title", "", 0, int64(1), true, publishedAt, publishedAt, locales)
}

func docsTestWriteBegin(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestDocsStaleRevisionRollsBackWithoutWrite(t *testing.T) {
	for _, action := range []string{"save", "publish", "unpublish"} {
		t.Run(action, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			docsTestWriteBegin(mock)
			mock.ExpectQuery(`(?s)SELECT n.id.*WHERE n.id=\$1`).WithArgs(int64(2)).WillReturnRows(docsTestNodeRows(t, 4))
			mock.ExpectRollback()
			repo := NewDocsRepository(db)
			if action == "save" {
				_, err = repo.SaveDraft(context.Background(), 2, service.DocDraftInput{Locale: "zh-CN", Revision: 3}, 42)
			} else {
				_, err = repo.Publish(context.Background(), 2, service.DocPublishInput{Locale: "zh-CN", Revision: 3}, 42, action == "publish")
			}
			require.ErrorIs(t, err, service.ErrDocsConflict)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestDocsDraftSaveIsAtomicAndPreservesPublicationMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	docsTestWriteBegin(mock)
	mock.ExpectQuery(`(?s)SELECT n.id.*WHERE n.id=\$1`).WithArgs(int64(2)).WillReturnRows(docsTestNodeRows(t, 4))
	mock.ExpectExec(`INSERT INTO custom_docs_contents\(node_id,locale,draft_title,draft_summary,draft_content_md,updated_by\)`).
		WithArgs(int64(2), "zh-CN", "New draft", "Summary", "Changed body", int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE custom_docs_nodes SET updated_at=NOW\(\),updated_by=\$2,revision=revision\+1 WHERE id=\$1`).
		WithArgs(int64(2), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT n.id.*WHERE n.id=\$1`).WithArgs(int64(2)).WillReturnRows(docsTestNodeRows(t, 5))
	mock.ExpectCommit()
	result, err := NewDocsRepository(db).SaveDraft(context.Background(), 2, service.DocDraftInput{Locale: "zh-CN", Title: "New draft", Summary: "Summary", ContentMD: "Changed body", Revision: 4}, 42)
	require.NoError(t, err)
	require.Equal(t, int64(5), result.Locales["zh-CN"].Revision)
	require.Equal(t, int64(2), *result.Locales["zh-CN"].PublishedRevision)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocsPublishCopiesSnapshotAndExcludesBase64FromSearch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	docsTestWriteBegin(mock)
	mock.ExpectQuery(`(?s)SELECT n.id.*WHERE n.id=\$1`).WithArgs(int64(2)).WillReturnRows(docsTestNodeRows(t, 4))
	mock.ExpectExec(`UPDATE custom_docs_contents SET published_title=draft_title,published_summary=draft_summary,published_content_md=draft_content_md,published_search_text=\$3,published_revision=revision\+1,revision=revision\+1`).
		WithArgs(int64(2), "zh-CN", "Body text", int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE custom_docs_nodes SET slug_locked=\(slug_locked OR \$3\)`).
		WithArgs(int64(2), int64(42), true).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT n.id.*WHERE n.id=\$1`).WithArgs(int64(2)).WillReturnRows(docsTestNodeRows(t, 5))
	mock.ExpectCommit()
	_, err = NewDocsRepository(db).Publish(context.Background(), 2, service.DocPublishInput{Locale: "zh-CN", Revision: 4}, 42, true)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocsTreeInvalidParentAndStaleSetRollBack(t *testing.T) {
	for _, invalidParent := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		docsTestWriteBegin(mock)
		mock.ExpectQuery(`SELECT id,kind FROM custom_docs_nodes`).WillReturnRows(sqlmock.NewRows([]string{"id", "kind"}).AddRow(int64(1), "group").AddRow(int64(2), "page"))
		mock.ExpectRollback()
		items := []service.DocTreeItem{{ID: 1}}
		want := service.ErrDocsConflict
		if invalidParent {
			pageID := int64(2)
			items = []service.DocTreeItem{{ID: 1, ParentID: &pageID}, {ID: 2}}
			want = service.ErrDocsInvalid
		}
		err = NewDocsRepository(db).SaveTree(context.Background(), items, 42)
		require.ErrorIs(t, err, want)
		require.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func TestDocsCannotDeletePublishedPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	docsTestWriteBegin(mock)
	mock.ExpectQuery(`(?s)SELECT n.id.*WHERE n.id=\$1`).WithArgs(int64(2)).WillReturnRows(docsTestNodeRows(t, 4))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM custom_docs_nodes WHERE parent_id=\$1\)`).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM custom_docs_contents WHERE node_id=\$1 AND published_revision IS NOT NULL\)`).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()
	require.ErrorIs(t, NewDocsRepository(db).Delete(context.Background(), 2), service.ErrDocsPublished)
	require.NoError(t, mock.ExpectationsWereMet())
}
