package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type videoTaskReleaseConflictRepo struct {
	UsageBillingRepository
	command *BatchImageBalanceHoldCommand
}

func (r *videoTaskReleaseConflictRepo) ReleaseBatchImageBalance(_ context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	r.command = cmd
	return nil, ErrUsageBillingRequestConflict
}

func TestVideoTaskBillingReleasePropagatesConflictsAndFencesZeroHolds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		hold    float64
	}{
		{"legacy", 0, 2}, {"v1", 1, 2}, {"v1_subscription_without_hold", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &videoTaskReleaseConflictRepo{}
			svc := &OpenAIGatewayService{usageBillingRepo: repo}
			lease := time.Now().Add(time.Minute)
			pending := &SeedanceVideoPendingBilling{IDVersion: tc.version, StateID: "vid_task", HoldID: "hold", HoldAmount: tc.hold,
				UserID: 42, APIKeyID: 7, LeaseUntil: &lease}
			err := svc.ReleaseSeedanceVideoBalance(context.Background(), pending)
			require.NotNil(t, repo.command)
			if tc.version == 0 {
				require.NoError(t, err)
				require.Empty(t, repo.command.VideoTaskStateID)
			} else {
				require.ErrorIs(t, err, ErrUsageBillingRequestConflict)
				require.Equal(t, pending.StateID, repo.command.VideoTaskStateID)
				require.Equal(t, pending.LeaseUntil, repo.command.VideoTaskLeaseUntil)
			}
		})
	}
}

func TestVideoTaskBillingLeaseMetadataDoesNotChangeFingerprint(t *testing.T) {
	legacy := &UsageBillingCommand{RequestID: "request", UserID: 42, APIKeyID: 7, BalanceCost: 0.75}
	current := *legacy
	lease := time.Now().Add(time.Minute)
	current.VideoTaskStateID, current.VideoTaskLeaseUntil = "vid_task", &lease
	legacy.Normalize()
	current.Normalize()
	require.Equal(t, legacy.RequestFingerprint, current.RequestFingerprint)
	before := &BatchImageBalanceHoldCommand{RequestID: "release", UserID: 42, APIKeyID: 7, BatchID: "hold", HoldAmount: 2}
	after := *before
	after.VideoTaskStateID, after.VideoTaskLeaseUntil = "vid_task", &lease
	before.Normalize()
	after.Normalize()
	require.Equal(t, before.RequestFingerprint, after.RequestFingerprint)
}
