package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestVideoTasksInitialCapabilitySelectionSkipsUnbillableSales(t *testing.T) {
	trusted, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	trusted.MeteringTrusted = true
	untrusted := trusted
	untrusted.MeteringTrusted = false
	for _, untrustedFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "untrusted_first", false: "trusted_first"}[untrustedFirst], func(t *testing.T) {
			row := explicitVideoPrice(trusted.Model, VideoBillingPerSecond, 0.1)
			svc, key := videoTasksGatewayPricingService(row)
			purchase := explicitVideoPrice(trusted.Model, VideoBillingPerVideo, 0.3)
			accounts := []Account{*videoTasksGatewayAccount(20, trusted, purchase), *videoTasksGatewayAccount(21, untrusted, purchase)}
			if untrustedFirst {
				accounts[0], accounts[1] = accounts[1], accounts[0]
			}
			svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
			body, err := json.Marshal(map[string]any{"model": trusted.Model, "prompt": "waves", "duration": trusted.DefaultDuration, "resolution": trusted.DefaultResolution})
			require.NoError(t, err)
			request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, body)
			require.NoError(t, err)
			require.True(t, info.Capability.MeteringTrusted)
			require.Equal(t, trusted.DefaultDuration, request.Duration)
		})
	}
}

func TestVideoTasksInitialCapabilitySelectionSkipsMissingActualSalesBound(t *testing.T) {
	unknown, ok := videoprovider.LookupVideoModelCapability("omni-fast", nil)
	require.True(t, ok)
	unknown.MeteringTrusted = true
	bounded := unknown
	bounded.DurationMode, bounded.MinDuration, bounded.MaxDuration, bounded.DefaultDuration = videoprovider.VideoDurationControlled, 1, 15, 6
	svc, key := videoTasksGatewayPricingService(explicitVideoPrice(unknown.Model, VideoBillingPerSecond, 0.1))
	purchase := explicitVideoPrice(unknown.Model, VideoBillingPerVideo, 0.3)
	svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*videoTasksGatewayAccount(20, unknown, purchase), *videoTasksGatewayAccount(21, bounded, purchase)}}
	body, err := json.Marshal(map[string]any{"model": unknown.Model, "prompt": "waves"})
	require.NoError(t, err)
	request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key, body)
	require.NoError(t, err)
	require.Equal(t, 15, info.Capability.MaxDuration)
	require.Equal(t, 6, request.Duration)
}

func TestVideoTasksAssignmentChecksFrozenSalesAgainstSelectedCapability(t *testing.T) {
	base, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	base.MeteringTrusted = true
	for _, tc := range []struct {
		name                 string
		unit                 VideoBillingUnit
		policy               VideoQuantityPolicy
		trusted              bool
		maximum              int
		subscription, reject bool
	}{
		{name: "untrusted_actual_sales_per_video_purchase", unit: VideoBillingPerSecond, policy: VideoQuantityOutputSeconds, maximum: 15, reject: true},
		{name: "larger_output_bound_exceeds_frozen_hold", unit: VideoBillingPerSecond, policy: VideoQuantityOutputSeconds, trusted: true, maximum: 30, reject: true},
		{name: "smaller_output_bound_fits", unit: VideoBillingPerSecond, policy: VideoQuantityOutputSeconds, trusted: true, maximum: 10},
		{name: "requested_seconds_do_not_require_actual_trust", unit: VideoBillingPerSecond, policy: VideoQuantityRequestedSeconds, maximum: 30},
		{name: "per_video_sales_do_not_require_actual_trust", unit: VideoBillingPerVideo, maximum: 30},
		{name: "subscription_keeps_zero_balance_hold", unit: VideoBillingPerSecond, policy: VideoQuantityOutputSeconds, trusted: true, maximum: 30, subscription: true},
		{name: "subscription_still_requires_actual_trust", unit: VideoBillingPerSecond, policy: VideoQuantityOutputSeconds, maximum: 15, subscription: true, reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := explicitVideoPrice(base.Model, tc.unit, 0.1)
			row.VideoQuantityPolicy = tc.policy
			svc, key := videoTasksGatewayPricingService(row)
			quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, SeedanceVideoRequestInfo{
				Model: base.Model, Resolution: base.DefaultResolution, DurationSeconds: base.DefaultDuration, Capability: &base})
			require.NoError(t, err)
			pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff", HoldID: "hold",
				UserID: 7, APIKeyID: 8, Model: base.Model, Resolution: base.DefaultResolution, DurationSeconds: base.DefaultDuration,
				SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount, IsSubscriptionBilling: tc.subscription}
			if tc.subscription {
				pending.HoldAmount = 0
			}
			repo := &videoTasksGatewayRepo{}
			svc.seedanceVideoTaskRepo = repo
			require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
			beforeSales, beforeHold, beforeDuration := *pending.SalesSnapshot, pending.HoldAmount, pending.DurationSeconds
			selected := base
			selected.MeteringTrusted, selected.MaxDuration, selected.AllowedDurations = tc.trusted, tc.maximum, nil
			account := videoTasksGatewayAccount(20, selected, explicitVideoPrice(base.Model, VideoBillingPerVideo, 0.3))
			err = svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, "video_tasks_json_v1")
			if tc.reject {
				require.ErrorIs(t, err, ErrVideoTaskSalesCapabilityMismatch)
				require.Zero(t, repo.tasks[pending.StateID].AccountID)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, beforeSales, *pending.SalesSnapshot)
			require.Equal(t, beforeHold, pending.HoldAmount)
			require.Equal(t, beforeDuration, pending.DurationSeconds)
		})
	}
}

func TestVideoTasksAssignmentRejectsLargerReferenceBoundWithoutIncreasingHold(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.5", nil)
	require.True(t, ok)
	row := explicitVideoPrice(capability.Model, VideoBillingPerSecond, 0.1)
	row.VideoQuantityPolicy = VideoQuantityOutputAndReferenceSeconds
	svc, key := videoTasksGatewayPricingService(row)
	quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, SeedanceVideoRequestInfo{
		Model: capability.Model, Resolution: "720p", DurationSeconds: 6, ReferenceVideoCount: 1, Capability: &capability})
	require.NoError(t, err)
	pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff", HoldID: "hold",
		UserID: 7, APIKeyID: 8, Model: capability.Model, Resolution: "720p", DurationSeconds: 6, ReferenceVideoCount: 1,
		SalesSnapshot: quote.SalesSnapshot, HoldAmount: quote.HoldAmount}
	svc.seedanceVideoTaskRepo = &videoTasksGatewayRepo{}
	require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), pending))
	capability.MaxReferenceVideoSeconds = 60
	account := videoTasksGatewayAccount(20, capability, explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.3))
	err = svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, "video_tasks_json_v1")
	require.ErrorIs(t, err, ErrVideoTaskSalesCapabilityMismatch)
	require.Equal(t, quote.HoldAmount, pending.HoldAmount)
	require.Zero(t, pending.AccountID)

	// An explicit sales ceiling is already part of the frozen contract. It
	// remains shared across suppliers instead of growing with account limits.
	pending.SalesSnapshot.MaxBillableSeconds = 60
	err = svc.AssignSeedanceVideoTaskAccount(context.Background(), pending, account, "video_tasks_json_v1")
	require.NoError(t, err)
	require.Equal(t, quote.HoldAmount, pending.HoldAmount)
}

func TestVideoTasksSalesActualQuantityNeedsFiniteOutputAndReferenceLimits(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	snapshot := &VideoTaskSalesSnapshot{Version: 1, BillingUnit: VideoBillingPerSecond, UnitPrice: 0.1, RateMultiplier: 1,
		QuantityPolicy: VideoQuantityOutputAndReferenceSeconds}
	_, err := videoTaskSalesHoldForCapability(snapshot, capability, 6, 1)
	require.ErrorContains(t, err, "reference video duration has no finite sales limit")
	capability.MaxReferenceVideoSeconds = 30
	_, err = videoTaskSalesHoldForCapability(snapshot, capability, 6, 1)
	require.NoError(t, err)
	capability.MaxDuration = 0
	_, err = videoTaskSalesHoldForCapability(snapshot, capability, 6, 1)
	require.ErrorContains(t, err, "output duration has no finite sales limit")
	snapshot.MaxBillableSeconds = 60
	amount, err := videoTaskSalesHoldForCapability(snapshot, capability, 6, 1)
	require.NoError(t, err)
	require.Equal(t, 6.0, amount)
}
