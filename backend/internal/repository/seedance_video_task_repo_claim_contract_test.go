//go:build unit

package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSeedanceVideoTaskRepositoryClaimSettlementAllowsExpiredLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewSeedanceVideoTaskRepository(db)
	claimSQL := `(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'processing', lease_until = \$4.*WHERE provider_task_id = \$1 AND user_id = \$2 AND api_key_id = \$3.*settlement_status = 'pending'.*settlement_status = 'processing' AND lease_until <= NOW\(\).*upstream_status.*cancel_requested`
	mock.ExpectExec(claimSQL).
		WithArgs("task-expired", int64(7), int64(8), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	claimed, err := repo.ClaimSettlement(context.Background(), "task-expired", 7, 8, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedanceVideoTaskRepositoryClaimSettlementRowsAffectedZeroIsNotClaimed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewSeedanceVideoTaskRepository(db)
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'processing'.*WHERE provider_task_id = \$1`).
		WithArgs("task-owned", int64(7), int64(8), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	claimed, err := repo.ClaimSettlement(context.Background(), "task-owned", 7, 8, time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedanceVideoTaskRepositoryClaimCancellationRejectsTerminalRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewSeedanceVideoTaskRepository(db)
	cancellationRepo, ok := repo.(service.SeedanceVideoTaskCancellationRepository)
	require.True(t, ok)
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'processing', upstream_status = 'cancel_requested'.*settlement_status = 'pending'\s+OR \(settlement_status = 'processing'.*lease_until IS NOT NULL.*lease_until <= NOW\(\).*LOWER\(TRIM\(COALESCE\(upstream_status, ''\)\)\) = 'cancel_requested'.*LOWER\(TRIM\(COALESCE\(upstream_status, ''\)\)\) NOT IN.*'completed'.*'succeeded'.*'canceled'.*'cancelled'`).
		WithArgs("task-terminal", int64(7), int64(8), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	claimed, err := cancellationRepo.ClaimCancellation(context.Background(), "task-terminal", 7, 8, time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedanceVideoTaskRepositoryClaimCancellationDoesNotStealExpiredProcessingLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewSeedanceVideoTaskRepository(db)
	cancellationRepo, ok := repo.(service.SeedanceVideoTaskCancellationRepository)
	require.True(t, ok)
	// A status poll may still be finalizing accounting after its lease expires.
	// DELETE may only reclaim an expired lease that already carries the durable
	// cancellation intent; an ordinary processing row must remain untouched.
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'processing', upstream_status = 'cancel_requested'.*WHERE provider_task_id = \$1 AND user_id = \$2 AND api_key_id = \$3.*settlement_status = 'pending'\s+OR \(settlement_status = 'processing'.*lease_until IS NOT NULL.*lease_until <= NOW\(\).*LOWER\(TRIM\(COALESCE\(upstream_status, ''\)\)\) = 'cancel_requested'`).
		WithArgs("task-processing", int64(7), int64(8), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	claimed, err := cancellationRepo.ClaimCancellation(context.Background(), "task-processing", 7, 8, time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedanceVideoTaskRepositoryLeaseAwareMutations(t *testing.T) {
	dueAt := time.Now().UTC().Add(time.Minute)
	leaseUntil := time.Now().UTC().Add(30 * time.Second)

	tests := []struct {
		name string
		sql  string
		args []driver.Value
		exec func(service.SeedanceVideoTaskRepository, context.Context) error
	}{
		{
			name: "reschedule",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*settlement_status = 'pending'.*WHERE state_id = \$1 AND settlement_status = 'processing'.*lease_until = \$5 AND lease_until > NOW\(\)`,
			args: []driver.Value{"state-1", "running", dueAt, "temporary error", leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository, ctx context.Context) error {
				leaseRepo := repo.(service.SeedanceVideoTaskRescheduleLeaseRepository)
				return leaseRepo.RescheduleWithLease(ctx, "state-1", "running", dueAt, "temporary error", leaseUntil)
			},
		},
		{
			name: "settle",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'settled'.*WHERE state_id = \$1 AND settlement_status = 'processing'.*lease_until = \$3 AND lease_until > NOW\(\)`,
			args: []driver.Value{"state-1", 8.4, leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository, ctx context.Context) error {
				leaseRepo := repo.(service.SeedanceVideoTaskSettlementLeaseRepository)
				return leaseRepo.MarkSettledWithLease(ctx, "state-1", 8.4, leaseUntil)
			},
		},
		{
			name: "release with status",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'released', upstream_status = \$2.*WHERE state_id = \$1 AND settlement_status = 'processing'.*lease_until = \$3 AND lease_until > NOW\(\)`,
			args: []driver.Value{"state-1", "canceled", leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository, ctx context.Context) error {
				leaseRepo := repo.(service.SeedanceVideoTaskReleaseStatusLeaseRepository)
				return leaseRepo.MarkReleasedWithStatusWithLease(ctx, "state-1", "canceled", leaseUntil)
			},
		},
		{
			name: "release intent",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*SET upstream_status = \$2, updated_at = NOW\(\).*WHERE state_id = \$1 AND settlement_status = 'processing'.*lease_until = \$3 AND lease_until > NOW\(\)`,
			args: []driver.Value{"state-1", "failed", leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository, ctx context.Context) error {
				leaseRepo := repo.(service.SeedanceVideoTaskReleaseIntentLeaseRepository)
				return leaseRepo.MarkReleaseIntentWithLease(ctx, "state-1", "failed", leaseUntil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := NewSeedanceVideoTaskRepository(db)
			mock.ExpectExec(tc.sql).WithArgs(tc.args...).WillReturnResult(sqlmock.NewResult(0, 1))
			require.NoError(t, tc.exec(repo, context.Background()))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSeedanceVideoTaskRepositoryLeaseAwareMutationsReturnNotFoundOnZeroRows(t *testing.T) {
	dueAt := time.Now().UTC().Add(time.Minute)
	leaseUntil := time.Now().UTC().Add(30 * time.Second)

	tests := []struct {
		name string
		sql  string
		args []driver.Value
		exec func(service.SeedanceVideoTaskRepository) error
	}{
		{
			name: "reschedule",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*settlement_status = 'pending'.*lease_until = \$5`,
			args: []driver.Value{"state-1", "running", dueAt, "temporary error", leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository) error {
				return repo.(service.SeedanceVideoTaskRescheduleLeaseRepository).RescheduleWithLease(context.Background(), "state-1", "running", dueAt, "temporary error", leaseUntil)
			},
		},
		{
			name: "settle",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'settled'.*lease_until = \$3`,
			args: []driver.Value{"state-1", 8.4, leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository) error {
				return repo.(service.SeedanceVideoTaskSettlementLeaseRepository).MarkSettledWithLease(context.Background(), "state-1", 8.4, leaseUntil)
			},
		},
		{
			name: "release with status",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*SET settlement_status = 'released', upstream_status = \$2.*lease_until = \$3`,
			args: []driver.Value{"state-1", "canceled", leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository) error {
				return repo.(service.SeedanceVideoTaskReleaseStatusLeaseRepository).MarkReleasedWithStatusWithLease(context.Background(), "state-1", "canceled", leaseUntil)
			},
		},
		{
			name: "release intent",
			sql:  `(?s)UPDATE custom_seedance_video_tasks.*SET upstream_status = \$2, updated_at = NOW\(\).*WHERE state_id = \$1 AND settlement_status = 'processing'.*lease_until = \$3 AND lease_until > NOW\(\)`,
			args: []driver.Value{"state-1", "failed", leaseUntil},
			exec: func(repo service.SeedanceVideoTaskRepository) error {
				return repo.(service.SeedanceVideoTaskReleaseIntentLeaseRepository).MarkReleaseIntentWithLease(context.Background(), "state-1", "failed", leaseUntil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := NewSeedanceVideoTaskRepository(db)
			mock.ExpectExec(tc.sql).WithArgs(tc.args...).WillReturnResult(sqlmock.NewResult(0, 0))
			require.ErrorIs(t, tc.exec(repo), service.ErrSeedanceVideoTaskNotFound)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
