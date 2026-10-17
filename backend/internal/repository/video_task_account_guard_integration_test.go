//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestVideoTaskAccountAssignmentSerializesCredentialRotation(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "video-task-credential-guard", Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "original-key", "base_url": "https://original.example/v1", "video_provider": "bblabu_v1"}})
	stateID, err := service.NewSeedanceVideoPublicTaskID()
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM custom_seedance_video_tasks WHERE state_id = $1", stateID)
		require.NoError(t, err)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, err = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
		require.NoError(t, err)
	})
	repo := NewSeedanceVideoTaskRepository(integrationDB)
	require.NoError(t, repo.Create(ctx, &service.SeedanceVideoPendingBilling{IDVersion: 1, StateID: stateID, HoldID: stateID, UserID: 7, APIKeyID: 8, Model: "seedance-2.0", Resolution: "480p", DurationSeconds: 4}))
	execution := &service.VideoTasksExecutionSnapshot{Version: 1, Protocol: "bblabu_v1", BaseURL: "https://original.example/v1", UpstreamModel: "Seedance-2.0"}
	mutation, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = mutation.Rollback() }()
	require.NoError(t, guardVideoAccountMutation(ctx, mutation, []int64{account.ID}, nil, map[string]any{"api_key": "rotated-key"}, false))
	assigned := make(chan error, 1)
	go func() {
		assigned <- repo.(service.SeedanceVideoTaskExecutionRepository).AssignAccountWithExecution(ctx, stateID, account.ID, "bblabu_v1", nil, execution, "original-key")
	}()
	select {
	case err := <-assigned:
		t.Fatalf("assignment bypassed account mutation lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	_, err = mutation.ExecContext(ctx, `UPDATE accounts SET credentials = jsonb_set(credentials,'{api_key}','"rotated-key"'::jsonb) WHERE id = $1`, account.ID)
	require.NoError(t, err)
	require.NoError(t, mutation.Commit())
	select {
	case err := <-assigned:
		require.ErrorContains(t, err, "identity changed")
	case <-time.After(5 * time.Second):
		t.Fatal("assignment did not finish after account mutation")
	}
	// After selecting the current credential, dependency creation wins the next
	// transaction; single, bulk and direct delete paths must now reject rotation.
	require.NoError(t, repo.(service.SeedanceVideoTaskExecutionRepository).AssignAccountWithExecution(ctx, stateID, account.ID, "bblabu_v1", nil, execution, "rotated-key"))
	accounts := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	require.ErrorIs(t, accounts.UpdateCredentials(ctx, account.ID, map[string]any{"api_key": "another-key"}), service.ErrVideoTaskCredentialsProtected)
	_, err = accounts.BulkUpdate(ctx, []int64{account.ID}, service.AccountBulkUpdate{Credentials: map[string]any{"api_key": "another-key"}})
	require.ErrorIs(t, err, service.ErrVideoTaskCredentialsProtected)
	require.ErrorIs(t, accounts.Delete(ctx, account.ID), service.ErrVideoTaskCredentialsProtected)
}
