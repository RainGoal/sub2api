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

func TestStudioHistoryPostgresPreservesSnapshotAndTerminalResult(t *testing.T) {
	ctx := context.Background()
	users := newUserRepositoryWithSQL(testEntClient(t), integrationDB)
	owner := &service.User{Email: fmt.Sprintf("studio-%d@example.com", time.Now().UnixNano()), PasswordHash: "test-hash", Role: service.RoleUser, Status: service.StatusActive, Concurrency: 1}
	require.NoError(t, users.Create(ctx, owner))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM custom_studio_tasks WHERE user_id = $1", owner.ID)
		require.NoError(t, err)
	})
	repo := NewStudioRepository(integrationDB)
	task := &service.StudioTask{ID: "task-1", Kind: "image", KeyID: 9, CreatedAt: 1000, Prompt: "original", Title: "First", Status: "completed", ImageResult: &service.StudioImageTask{ID: "task-1", TaskID: "task-1", Status: "completed", ImageURL: "https://assets.example/output.png"}}
	_, err := repo.Upsert(ctx, owner.ID, task)
	require.NoError(t, err)
	// A stale tab may change mutable labels but cannot regress status or inputs.
	task.Prompt, task.CreatedAt, task.Title, task.Status, task.ImageResult = "changed", 2000, "Selected", "processing", nil
	task.Adopted = []string{"https://assets.example/output.png"}
	saved, err := repo.Upsert(ctx, owner.ID, task)
	require.NoError(t, err)
	require.Equal(t, "original", saved.Prompt)
	require.Equal(t, int64(1000), saved.CreatedAt)
	require.Equal(t, "Selected", saved.Title)
	require.Equal(t, "completed", saved.Status)
	require.NotNil(t, saved.ImageResult)
	require.Len(t, saved.Adopted, 1)
	_, err = repo.Get(ctx, owner.ID+1, "image", "task-1")
	require.ErrorIs(t, err, service.ErrStudioTaskNotFound)
	task.KeyID = 10
	_, err = repo.Upsert(ctx, owner.ID, task)
	require.Error(t, err)
	items, err := repo.List(ctx, owner.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(9), items[0].KeyID)
}
