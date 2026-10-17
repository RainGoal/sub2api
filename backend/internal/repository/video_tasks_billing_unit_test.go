//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const videoBillingLockSQL = `(?s)SELECT settlement_status, provider_task_id, lease_until, actual_cost.*FROM custom_seedance_video_tasks.*FOR UPDATE`
const videoBillingExistsSQL = `(?s)SELECT EXISTS \(.*FROM usage_billing_dedup WHERE request_id = \$1 AND api_key_id = \$2.*UNION ALL.*FROM usage_billing_dedup_archive WHERE request_id = \$1 AND api_key_id = \$2.*\)`

func expectVideoBillingLock(mock sqlmock.Sqlmock, lease time.Time, cost any) {
	mock.ExpectQuery(videoBillingLockSQL).WithArgs("vid_task", int64(42), int64(7), "hold", 2.0).
		WillReturnRows(sqlmock.NewRows([]string{"settlement_status", "provider_task_id", "lease_until", "actual_cost"}).
			AddRow("processing", "supplier-task", lease, cost))
	mock.ExpectQuery(`SELECT clock_timestamp\(\)`).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(lease.Add(-time.Minute)))
}

func TestVideoTaskBillingReleaseAfterCaptureReconcilesWithoutRefund(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	lease := time.Now().Add(time.Minute)
	mock.ExpectBegin()
	expectVideoBillingLock(mock, lease, 0.75)
	mock.ExpectQuery(videoBillingExistsSQL).
		WithArgs(service.StableSeedanceVideoBillingRequestID("vid_task"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*settlement_status = 'settled'.*upstream_status = 'completed'.*WHERE state_id = \$1`).
		WithArgs("vid_task").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	_, err = (&usageBillingRepository{db: db}).ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID: service.BatchImageReleaseRequestID("hold"), UserID: 42, APIKeyID: 7,
		BatchID: "hold", HoldAmount: 2, VideoTaskStateID: "vid_task", VideoTaskLeaseUntil: &lease,
	})
	require.ErrorIs(t, err, service.ErrVideoTaskAlreadyCaptured)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoTaskBillingCaptureCommitsPaidAmountWithDedup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	lease := time.Now().Add(time.Minute)
	requestID := service.StableSeedanceVideoBillingRequestID("vid_task")
	mock.ExpectBegin()
	expectVideoBillingLock(mock, lease, nil)
	mock.ExpectQuery(videoBillingExistsSQL).WithArgs(service.BatchImageReleaseRequestID("hold"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup.*RETURNING id`).WithArgs(requestID, int64(7), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT request_fingerprint.*FROM usage_billing_dedup_archive`).WithArgs(requestID, int64(7)).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`(?s)SELECT 1.*FROM usage_billing_dedup.*WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("hold"), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectQuery(captureUsageBalanceHoldSQL).WithArgs(2.0, 0.75000001, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(97.24999999))
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*SET actual_cost = \$2.*WHERE state_id = \$1`).
		WithArgs("vid_task", 0.75000001).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := (&usageBillingRepository{db: db}).Apply(context.Background(), &service.UsageBillingCommand{
		RequestID: requestID, UserID: 42, APIKeyID: 7, BalanceCost: 0.750000006,
		BalanceHoldID: "hold", BalanceHoldAmount: 2, VideoTaskStateID: "vid_task", VideoTaskLeaseUntil: &lease,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoTaskBillingCaptureRejectsCommittedRelease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	lease := time.Now().Add(time.Minute)
	mock.ExpectBegin()
	expectVideoBillingLock(mock, lease, nil)
	mock.ExpectQuery(videoBillingExistsSQL).WithArgs(service.BatchImageReleaseRequestID("hold"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()
	_, err = (&usageBillingRepository{db: db}).Apply(context.Background(), &service.UsageBillingCommand{
		RequestID: service.StableSeedanceVideoBillingRequestID("vid_task"), UserID: 42, APIKeyID: 7,
		BalanceHoldID: "hold", BalanceHoldAmount: 2, VideoTaskStateID: "vid_task", VideoTaskLeaseUntil: &lease,
	})
	require.ErrorIs(t, err, service.ErrVideoTaskAlreadyReleased)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoTaskBillingReleaseRefusesCaptureWithoutDurableAmount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	lease := time.Now().Add(time.Minute)
	mock.ExpectBegin()
	expectVideoBillingLock(mock, lease, nil)
	mock.ExpectQuery(videoBillingExistsSQL).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()
	_, err = (&usageBillingRepository{db: db}).ReleaseBatchImageBalance(context.Background(), &service.BatchImageBalanceHoldCommand{
		RequestID: service.BatchImageReleaseRequestID("hold"), UserID: 42, APIKeyID: 7,
		BatchID: "hold", HoldAmount: 2, VideoTaskStateID: "vid_task", VideoTaskLeaseUntil: &lease,
	})
	require.ErrorContains(t, err, "no durable paid amount")
	require.NotErrorIs(t, err, service.ErrVideoTaskAlreadyCaptured)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoTaskBillingUnboundReleaseAndLeaseFencing(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		providerID   any
		lease        *time.Time
		valid        bool
	}{
		{name: "unbound_create_failure", status: "pending", valid: true},
		{name: "accepted_task_requires_claim", status: "pending", providerID: "supplier-task"},
		{name: "manual_review_cannot_release_unclaimed", status: "needs_review"},
		{name: "stale_worker", status: "processing", providerID: "supplier-task", lease: new(time.Time)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectQuery(videoBillingLockSQL).WillReturnRows(sqlmock.NewRows([]string{"settlement_status", "provider_task_id", "lease_until", "actual_cost"}).
				AddRow(tc.status, tc.providerID, nil, nil))
			if tc.valid {
				mock.ExpectQuery(videoBillingExistsSQL).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
				mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup.*RETURNING id`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
				mock.ExpectQuery(`(?s)SELECT request_fingerprint.*FROM usage_billing_dedup_archive`).WillReturnError(sql.ErrNoRows)
				mock.ExpectQuery(`(?s)SELECT 1.*FROM usage_billing_dedup.*WHERE request_id`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
				mock.ExpectQuery(releaseBatchImageHoldSQL).WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(98, 2))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			_, err = (&usageBillingRepository{db: db}).ReleaseBatchImageBalance(context.Background(), &service.BatchImageBalanceHoldCommand{
				RequestID: service.BatchImageReleaseRequestID("hold"), UserID: 42, APIKeyID: 7,
				BatchID: "hold", HoldAmount: 2, VideoTaskStateID: "vid_task", VideoTaskLeaseUntil: tc.lease,
			})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, service.ErrSeedanceVideoLeaseMutationUnsupported)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoTaskBillingExpiredLeaseCannotReleaseAfterWaitingForLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	lease := time.Now().Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery(videoBillingLockSQL).WillReturnRows(sqlmock.NewRows([]string{"settlement_status", "provider_task_id", "lease_until", "actual_cost"}).
		AddRow("processing", "supplier-task", lease, nil))
	mock.ExpectQuery(`SELECT clock_timestamp\(\)`).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(lease.Add(time.Second)))
	mock.ExpectRollback()
	_, err = (&usageBillingRepository{db: db}).ReleaseBatchImageBalance(context.Background(), &service.BatchImageBalanceHoldCommand{
		RequestID: service.BatchImageReleaseRequestID("hold"), UserID: 42, APIKeyID: 7,
		BatchID: "hold", HoldAmount: 2, VideoTaskStateID: "vid_task", VideoTaskLeaseUntil: &lease,
	})
	require.ErrorIs(t, err, service.ErrSeedanceVideoLeaseMutationUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoTaskBillingReconciliationCommitFailureDoesNotSignalSettled(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	lease := time.Now().Add(time.Minute)
	mock.ExpectBegin()
	expectVideoBillingLock(mock, lease, 0.75)
	mock.ExpectQuery(videoBillingExistsSQL).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*settlement_status = 'settled'`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(sql.ErrConnDone)
	_, err = (&usageBillingRepository{db: db}).ReleaseBatchImageBalance(context.Background(), &service.BatchImageBalanceHoldCommand{
		RequestID: service.BatchImageReleaseRequestID("hold"), UserID: 42, APIKeyID: 7,
		BatchID: "hold", HoldAmount: 2, VideoTaskStateID: "vid_task", VideoTaskLeaseUntil: &lease,
	})
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NotErrorIs(t, err, service.ErrVideoTaskAlreadyCaptured)
	require.NoError(t, mock.ExpectationsWereMet())
}
