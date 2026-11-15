//go:build unit

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

func TestVideoTaskReviewSuccessQueuesAtomicallyUnderOwnedLease(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		lease := time.Now().Add(time.Minute)
		decision := service.VideoTaskReviewDecision{ActorID: 42, Action: "confirmed_succeeded", Reason: "supplier verified", DecidedAt: time.Now(),
			Metering: &service.VideoTaskReviewMetering{OutputSeconds: 8.5, ReferenceSeconds: 2.25}}
		encoded, err := json.Marshal(decision)
		require.NoError(t, err)
		mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*review_decisions = review_decisions.*settlement_status = 'pending', upstream_status = 'completed', review_reason = ''.*next_poll_at = NOW\(\), lease_until = NULL.*WHERE state_id = \$1 AND settlement_status = 'processing' AND provider_task_id IS NOT NULL.*lease_until = \$3 AND lease_until > NOW\(\).*NOT IN.*'failed'.*'cancel_requested'`).
			WithArgs("vid_task", string(encoded), lease).WillReturnResult(sqlmock.NewResult(0, affected))
		repo := NewSeedanceVideoTaskRepository(db).(service.SeedanceVideoTaskReviewRepository)
		err = repo.RecordReviewDecisionWithLease(context.Background(), "vid_task", decision, lease)
		if affected == 0 {
			require.ErrorIs(t, err, service.ErrSeedanceVideoTaskNotFound)
		} else {
			require.NoError(t, err)
		}
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestVideoTaskReviewSuccessExcludesClientSettlementAndCancellationClaims(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectExec(`(?s)UPDATE custom_seedance_video_tasks.*COALESCE\(review_decisions -> -1 ->> 'action', ''\) <> 'confirmed_succeeded'`).
			WithArgs("vid_task", int64(7), int64(8), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
		repo := NewSeedanceVideoTaskRepository(db)
		var claimed bool
		if cancel {
			claimed, err = repo.(service.SeedanceVideoTaskCancellationRepository).ClaimCancellation(context.Background(), "vid_task", 7, 8, time.Minute)
		} else {
			claimed, err = repo.ClaimSettlement(context.Background(), "vid_task", 7, 8, time.Minute)
		}
		require.NoError(t, err)
		require.False(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
