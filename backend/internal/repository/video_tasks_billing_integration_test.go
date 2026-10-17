//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func videoBillingFixture(t *testing.T) (service.UsageBillingRepository, *service.SeedanceVideoPendingBilling) {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("video-billing-%s@example.com", uuid.NewString()), PasswordHash: "hash", Balance: 100})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-video-billing-" + uuid.NewString(), Name: "video-billing"})
	account := mustCreateAccount(t, client, &service.Account{Name: "video-billing", Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "billing-key", "base_url": "https://original.example/v1", "video_provider": "bblabu_v1"}})
	stateID, err := service.NewSeedanceVideoPublicTaskID()
	require.NoError(t, err)
	task := &service.SeedanceVideoPendingBilling{IDVersion: 1, StateID: stateID, HoldID: stateID, UserID: user.ID, APIKeyID: key.ID,
		Model: "seedance-2.0", Resolution: "480p", DurationSeconds: 4, HoldAmount: 2}
	tasks := NewSeedanceVideoTaskRepository(integrationDB)
	require.NoError(t, tasks.Create(ctx, task))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM custom_seedance_video_tasks WHERE state_id = $1", stateID)
		require.NoError(t, err)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, err = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
		require.NoError(t, err)
	})
	for _, holdID := range []string{stateID, stateID + "-other"} {
		_, err := repo.ReserveBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
			RequestID: service.BatchImageHoldRequestID(holdID), UserID: user.ID, APIKeyID: key.ID, BatchID: holdID, HoldAmount: 2})
		require.NoError(t, err)
	}
	execution := &service.VideoTasksExecutionSnapshot{Version: 1, Protocol: "bblabu_v1", BaseURL: "https://original.example/v1", UpstreamModel: "Seedance-2.0"}
	require.NoError(t, tasks.(service.SeedanceVideoTaskExecutionRepository).AssignAccountWithExecution(ctx, stateID, account.ID, "bblabu_v1", nil, execution, "billing-key"))
	require.NoError(t, tasks.BindProviderTask(ctx, stateID, "supplier-"+stateID, "completed", time.Now()))
	claimed, err := tasks.ClaimSettlement(ctx, stateID, user.ID, key.ID, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	task, err = tasks.GetByProviderTask(ctx, stateID, user.ID, key.ID)
	require.NoError(t, err)
	return repo, task
}

func videoCaptureCommand(task *service.SeedanceVideoPendingBilling) *service.UsageBillingCommand {
	return &service.UsageBillingCommand{RequestID: service.StableSeedanceVideoBillingRequestID(task.StateID), UserID: task.UserID, APIKeyID: task.APIKeyID,
		BalanceCost: 0.75, BalanceHoldID: task.HoldID, BalanceHoldAmount: task.HoldAmount, VideoTaskStateID: task.StateID, VideoTaskLeaseUntil: task.LeaseUntil}
}

func videoReleaseCommand(task *service.SeedanceVideoPendingBilling) *service.BatchImageBalanceHoldCommand {
	return &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageReleaseRequestID(task.HoldID), UserID: task.UserID, APIKeyID: task.APIKeyID,
		BatchID: task.HoldID, HoldAmount: task.HoldAmount, VideoTaskStateID: task.StateID, VideoTaskLeaseUntil: task.LeaseUntil}
}

func TestVideoTaskBillingPostgresCaptureThenReleaseDoesNotConsumeAnotherHold(t *testing.T) {
	ctx := context.Background()
	repo, task := videoBillingFixture(t)
	_, err := repo.Apply(ctx, videoCaptureCommand(task))
	require.NoError(t, err)
	// Simulate losing the separate task settlement write after money committed.
	_, err = integrationDB.ExecContext(ctx, `UPDATE custom_seedance_video_tasks SET upstream_status = 'failed' WHERE state_id = $1`, task.StateID)
	require.NoError(t, err)
	_, err = repo.ReleaseBatchImageBalance(ctx, videoReleaseCommand(task))
	require.ErrorIs(t, err, service.ErrVideoTaskAlreadyCaptured)
	assertVideoBillingFunds(t, task, 97.25)
	var status, upstream string
	var amount float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT settlement_status, upstream_status, actual_cost FROM custom_seedance_video_tasks WHERE state_id = $1`, task.StateID).
		Scan(&status, &upstream, &amount))
	require.Equal(t, "settled", status)
	require.Equal(t, "completed", upstream)
	require.Equal(t, 0.75, amount)
	var releaseKeys int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2`,
		service.BatchImageReleaseRequestID(task.HoldID), task.APIKeyID).Scan(&releaseKeys))
	require.Zero(t, releaseKeys)
}

func TestVideoTaskBillingPostgresCaptureAndReleaseSerialize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, task := videoBillingFixture(t)
	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.ExecContext(ctx, `SELECT state_id FROM custom_seedance_video_tasks WHERE state_id = $1 FOR UPDATE`, task.StateID)
	require.NoError(t, err)
	captureDone, releaseDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := repo.Apply(ctx, videoCaptureCommand(task)); captureDone <- err }()
	go func() { _, err := repo.ReleaseBatchImageBalance(ctx, videoReleaseCommand(task)); releaseDone <- err }()
	select {
	case err := <-captureDone:
		t.Fatalf("capture bypassed video row lock: %v", err)
	case err := <-releaseDone:
		t.Fatalf("release bypassed video row lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, blocker.Commit())
	captureErr, releaseErr := <-captureDone, <-releaseDone
	if captureErr == nil {
		require.ErrorIs(t, releaseErr, service.ErrVideoTaskAlreadyCaptured)
		assertVideoBillingFunds(t, task, 97.25)
	} else {
		require.NoError(t, releaseErr)
		require.ErrorIs(t, captureErr, service.ErrVideoTaskAlreadyReleased)
		assertVideoBillingFunds(t, task, 98)
	}
}

func assertVideoBillingFunds(t *testing.T, task *service.SeedanceVideoPendingBilling, expectedBalance float64) {
	t.Helper()
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT balance, frozen_balance FROM users WHERE id = $1`, task.UserID).Scan(&balance, &frozen))
	require.InDelta(t, expectedBalance, balance, 1e-8)
	require.Equal(t, 2.0, frozen, "the unrelated task's hold must be preserved")
}
