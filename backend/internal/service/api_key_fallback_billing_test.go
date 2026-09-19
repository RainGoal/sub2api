//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestFallbackBillingCountsUserOnceAndChecksDestinationOverride(t *testing.T) {
	zero, fallbackLimit := 0, 1
	rpm := &userRPMCacheStub{userGroupCounts: []int{1, 2}, userCounts: []int{1, 2}}
	repo := &rpmOverrideRepoStub{override: &fallbackLimit}
	cache := &balanceEligibilityCacheStub{balance: 10}
	svc := NewBillingCacheService(cache, nil, nil, nil, rpm, repo, &config.Config{}, nil)
	t.Cleanup(svc.Stop)
	user := &User{ID: 7, RPMLimit: 1, UserGroupRPMOverride: &zero}
	primary := &Group{ID: 10, RPMLimit: 10}
	backup := &Group{ID: 20, RPMLimit: 10}
	key := &APIKey{ID: 5, User: user, GroupID: &primary.ID, Group: primary}

	require.NoError(t, svc.CheckBillingEligibility(context.Background(), user, key, primary, nil, ""))
	require.NoError(t, svc.CheckFallbackBillingEligibility(context.Background(), user, key, backup, ""))
	require.ErrorIs(t, svc.CheckFallbackBillingEligibility(context.Background(), user, key, backup, ""), ErrGroupRPMExceeded)
	require.EqualValues(t, 1, atomic.LoadInt32(&rpm.userCalls))
	require.EqualValues(t, 2, atomic.LoadInt32(&rpm.userGroupCalls))
	require.EqualValues(t, 2, atomic.LoadInt32(&repo.calls))
	require.Equal(t, 1, user.RPMLimit)
	require.Same(t, &zero, user.UserGroupRPMOverride)
}

func TestFallbackBillingStillEnforcesBalanceAndCancellation(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0}
	rpm := &userRPMCacheStub{}
	svc := NewBillingCacheService(cache, nil, nil, nil, rpm, nil, &config.Config{}, nil)
	t.Cleanup(svc.Stop)
	user := &User{ID: 1, RPMLimit: 1}
	group := &Group{ID: 2, RPMLimit: 1}
	require.ErrorIs(t, svc.CheckFallbackBillingEligibility(context.Background(), user, nil, group, ""), ErrInsufficientBalance)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, svc.CheckFallbackBillingEligibility(ctx, user, nil, group, ""), context.Canceled)
	require.Zero(t, atomic.LoadInt32(&rpm.userCalls))
	require.Zero(t, atomic.LoadInt32(&rpm.userGroupCalls))
}
