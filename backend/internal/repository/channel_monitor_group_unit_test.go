//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorGroupNameUsesCurrentGroupAndKeepsLegacyCompatibility(t *testing.T) {
	row := &dbent.ChannelMonitor{GroupName: "legacy"}
	require.Equal(t, "legacy", entToServiceMonitor(row).GroupName)

	id := int64(17)
	row.GroupID = &id
	row.Edges.Group = &dbent.Group{ID: id, Name: "current"}
	require.Equal(t, "current", entToServiceMonitor(row).GroupName)
	row.Edges.Group.Name = "renamed"
	require.Equal(t, "renamed", entToServiceMonitor(row).GroupName)

	now := time.Now()
	row.Edges.Group.DeletedAt = &now
	require.Empty(t, entToServiceMonitor(row).GroupName, "a deleted binding must not fall back to a stale copied name")
	require.Nil(t, entToServiceMonitor(row).GroupID, "deleted groups must not expose navigation targets")
	row.Edges.Group = nil
	require.Empty(t, entToServiceMonitor(row).GroupName)
	require.Nil(t, entToServiceMonitor(row).GroupID)
	require.Equal(t, id, *row.GroupID, "response mapping must not mutate the stored binding")

	monitor := &service.ChannelMonitor{GroupID: &id, GroupName: "resolved"}
	require.Empty(t, monitorGroupNameForPersistence(monitor))
	monitor.GroupID = nil
	require.Equal(t, "resolved", monitorGroupNameForPersistence(monitor))
}

func TestChannelMonitorRejectsMissingOrDeletedGroupBeforeWriting(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := NewChannelMonitorRepository(client, db)
	id := int64(999)
	for _, write := range []func(context.Context, *service.ChannelMonitor) error{repo.Create, repo.Update} {
		mock.ExpectQuery(`SELECT .* FROM "groups" WHERE .*"groups"\."id" = \$1 AND "groups"\."deleted_at" IS NULL.* LIMIT 2`).
			WithArgs(id).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
		require.ErrorIs(t, write(context.Background(), &service.ChannelMonitor{GroupID: &id}), service.ErrChannelMonitorInvalidGroup)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
