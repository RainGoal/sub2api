//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyFallbackPersistenceAndGroupDeletion(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	users := mustCreateUser(t, client, &service.User{Email: "fallback-key@example.com"})
	primary := mustCreateGroup(t, client, &service.Group{Name: "fallback-primary"})
	fallback := mustCreateGroup(t, client, &service.Group{Name: "fallback-backup"})
	repo := newAPIKeyRepositoryWithSQL(client, tx)
	key := &service.APIKey{UserID: users.ID, Name: "fallback", Key: "sk-fallback-persistence", Status: service.StatusActive,
		GroupID: &primary.ID, FallbackGroupID: &fallback.ID, QuotaUsed: 5, Usage5h: 3}
	require.NoError(t, repo.Create(ctx, key))
	loaded, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, key.FallbackGroupID, loaded.FallbackGroupID)
	keys, err := repo.ListKeysByGroupID(ctx, fallback.ID)
	require.NoError(t, err)
	require.Contains(t, keys, key.Key)

	// Clearing only fallback must preserve concurrent quota accounting and primary.
	_, err = repo.IncrementQuotaUsed(ctx, key.ID, 2)
	require.NoError(t, err)
	key.FallbackGroupID = nil
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{FallbackGroupID: true}))
	loaded, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Nil(t, loaded.FallbackGroupID)
	require.Equal(t, 7.0, loaded.QuotaUsed)
	require.Equal(t, key.GroupID, loaded.GroupID)

	key.FallbackGroupID = &fallback.ID
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{FallbackGroupID: true}))
	groupRepo := newGroupRepositoryWithSQL(client, tx)
	_, err = groupRepo.DeleteCascade(ctx, fallback.ID)
	require.NoError(t, err)
	loaded, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Nil(t, loaded.FallbackGroupID)
	require.Equal(t, key.GroupID, loaded.GroupID)
}
