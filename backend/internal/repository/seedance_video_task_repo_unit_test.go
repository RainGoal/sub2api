//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var seedanceVideoTaskTestColumns = []string{
	"state_id", "provider_task_id", "user_id", "api_key_id", "group_id",
	"account_id", "provider_protocol", "model", "resolution", "duration_seconds", "reference_video_count",
	"original_model", "request_payload_hash", "hold_id", "hold_amount",
	"total_cost_per_second", "actual_cost_per_second", "rate_multiplier",
	"is_subscription_billing", "subscription_id", "upstream_status", "settlement_status",
	"retry_count", "next_poll_at", "lease_until", "last_error_message", "created_at",
}

func seedanceVideoTaskTestRow(now time.Time, settlement string, lease any) *sqlmock.Rows {
	return sqlmock.NewRows(seedanceVideoTaskTestColumns).AddRow(
		"state-1", "task-1", int64(7), int64(8), int64(9), int64(20),
		"bblabu_v1", "Seedance-2.5", "720p", 30, 1, "Seedance-2.5", "payload-hash",
		"seedance:hold-1", 6.0, 0.1, 0.2, 2.0, false, nil,
		"queued", settlement, 0, now, lease, "", now.Add(-time.Minute),
	)
}

func TestSeedanceVideoTaskRepositoryCreateBindAndOwnerLookup(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewSeedanceVideoTaskRepository(db)
	groupID := int64(9)
	now := time.Now().UTC().Truncate(time.Millisecond)
	pending := &service.SeedanceVideoPendingBilling{
		StateID: "state-1", UserID: 7, APIKeyID: 8, GroupID: &groupID, AccountID: 20,
		ProviderID: "bblabu_v1",
		Model:      "Seedance-2.5", Resolution: "720p", DurationSeconds: 30,
		ReferenceVideoCount: 1, OriginalModel: "Seedance-2.5", CreatedAt: now.Format(time.RFC3339Nano),
		RequestPayloadHash: "payload-hash", HoldID: "seedance:hold-1", HoldAmount: 6,
		TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.2, RateMultiplier: 2,
		NextPollAt: now.Add(time.Minute),
	}

	mock.ExpectExec(`(?s)INSERT INTO custom_seedance_video_tasks`).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.Create(ctx, pending))
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks\s+SET account_id = \$2`).
		WithArgs("state-1", int64(20), "bblabu_v1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.AssignAccount(ctx, "state-1", 20, "bblabu_v1"))
	dueAt := now.Add(4 * time.Second)
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks\s+SET provider_task_id = \$2`).
		WithArgs("state-1", "task-1", "queued", dueAt).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.BindProviderTask(ctx, "state-1", "task-1", "queued", dueAt))
	mock.ExpectQuery(`(?s)FROM custom_seedance_video_tasks\s+WHERE provider_task_id = \$1 AND user_id = \$2 AND api_key_id = \$3`).
		WithArgs("task-1", int64(7), int64(8)).WillReturnRows(seedanceVideoTaskTestRow(now, "pending", nil))

	loaded, err := repo.GetByProviderTask(ctx, "task-1", 7, 8)
	require.NoError(t, err)
	require.Equal(t, int64(20), loaded.AccountID)
	require.Equal(t, "bblabu_v1", loaded.ProviderID)
	require.Equal(t, "Seedance-2.5", loaded.Model)
	require.Equal(t, &groupID, loaded.GroupID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedanceVideoTaskRepositoryClaimDueUsesDatabaseLease(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewSeedanceVideoTaskRepository(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	leaseUntil := now.Add(30 * time.Second)
	claimSQL := `(?s)WHERE \(settlement_status = 'pending' AND next_poll_at <= \$1\)\s+OR \(settlement_status = 'processing' AND lease_until <= \$1\).*FOR UPDATE SKIP LOCKED`
	mock.ExpectQuery(claimSQL).WithArgs(now, 20, leaseUntil).
		WillReturnRows(seedanceVideoTaskTestRow(now, "processing", leaseUntil))

	claimed, err := repo.ClaimDue(ctx, now, 30*time.Second, 20)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, service.SeedanceVideoSettlementProcessing, claimed[0].SettlementStatus)
	require.Equal(t, leaseUntil, *claimed[0].LeaseUntil)

	// A second replica running the same SKIP LOCKED claim sees no row while the
	// first lease is active; the database, not process memory, is the mutex.
	mock.ExpectQuery(claimSQL).WithArgs(now, 20, leaseUntil).
		WillReturnRows(sqlmock.NewRows(seedanceVideoTaskTestColumns))
	secondClaim, err := repo.ClaimDue(ctx, now, 30*time.Second, 20)
	require.NoError(t, err)
	require.Empty(t, secondClaim)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedanceVideoTaskRepositorySettlementMutations(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewSeedanceVideoTaskRepository(db)
	dueAt := time.Now().UTC().Add(time.Minute)

	mock.ExpectExec(`(?s)SET upstream_status = CASE.*settlement_status = 'pending'`).
		WithArgs("state-1", "in_progress", dueAt, "temporary error").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Reschedule(ctx, "state-1", "in_progress", dueAt, "temporary error"))

	mock.ExpectExec(`(?s)SET settlement_status = 'settled'.*actual_cost = \$2`).
		WithArgs("state-1", 8.4).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkSettled(ctx, "state-1", 8.4))

	mock.ExpectExec(`(?s)SET settlement_status = 'processing', lease_until = \$4.*settlement_status = 'pending'`).
		WithArgs("task-1", int64(7), int64(8), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	claimed, err := repo.ClaimSettlement(ctx, "task-1", 7, 8, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)

	mock.ExpectExec(`(?s)SET settlement_status = 'processing', upstream_status = 'cancel_requested'.*settlement_status = 'pending'`).
		WithArgs("task-2", int64(7), int64(8), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	cancellationRepo, ok := repo.(service.SeedanceVideoTaskCancellationRepository)
	require.True(t, ok)
	claimed, err = cancellationRepo.ClaimCancellation(ctx, "task-2", 7, 8, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)

	mock.ExpectExec(`(?s)SET settlement_status = 'released'`).
		WithArgs("state-2").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkReleased(ctx, "state-2"))

	mock.ExpectExec(`(?s)SET settlement_status = 'released', upstream_status = \$2`).
		WithArgs("state-3", "canceled").WillReturnResult(sqlmock.NewResult(0, 1))
	releaseRepo, ok := repo.(service.SeedanceVideoTaskReleaseStatusRepository)
	require.True(t, ok)
	require.NoError(t, releaseRepo.MarkReleasedWithStatus(ctx, "state-3", "canceled"))

	mock.ExpectExec(`(?s)SET settlement_status = 'released'`).
		WithArgs("missing").WillReturnResult(sqlmock.NewResult(0, 0))
	err = repo.MarkReleased(ctx, "missing")
	require.True(t, errors.Is(err, service.ErrSeedanceVideoTaskNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}
