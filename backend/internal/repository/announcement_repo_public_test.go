package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func TestAnnouncementRepositoryListPublicActive(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:announcement_repo_public?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	repo := NewAnnouncementRepository(client)
	now := time.Date(2026, time.November, 29, 0, 0, 0, 0, time.UTC)
	older := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	create := func(title, status string, isPublic bool, createdAt time.Time, startsAt, endsAt *time.Time, rawTargeting string) *dbent.Announcement {
		t.Helper()
		item, err := client.Announcement.Create().
			SetTitle(title).SetContent("Content").SetStatus(status).SetIsPublic(isPublic).
			SetCreatedAt(createdAt).SetNillableStartsAt(startsAt).SetNillableEndsAt(endsAt).
			Save(ctx)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "UPDATE announcements SET targeting = ? WHERE id = ?", rawTargeting, item.ID)
		require.NoError(t, err)
		return item
	}

	active := service.AnnouncementStatusActive
	first := create("Starts now", active, true, older, &now, &future, "{}")
	second := create("Same created_at", active, true, older, nil, nil, `{"any_of":[]}`)
	third := create("More recent", active, true, now, nil, nil, `{"any_of":null}`)
	create("Targeted", active, true, now, nil, nil, `{"any_of":[{"all_of":[{"type":"balance","operator":"gt","value":100}]}]}`)
	create("Malformed targeting", active, true, now, nil, nil, `{"any_of":"[]"}`)
	create("Private", active, false, now, nil, nil, "{}")
	create("Draft", service.AnnouncementStatusDraft, true, now, nil, nil, "{}")
	create("Expired", active, true, now, nil, &now, "{}")
	create("Future", active, true, now, &future, nil, "{}")

	items, err := repo.ListPublicActive(ctx, now, 2)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, third.ID, items[0].ID)
	require.Equal(t, second.ID, items[1].ID)
	require.True(t, items[0].IsPublic)

	items, err = repo.ListPublicActive(ctx, now, 50)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, first.ID, items[2].ID)
}
