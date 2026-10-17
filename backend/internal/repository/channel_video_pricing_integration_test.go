//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelVideoPricingResolutionSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	defaultPrice, resolutionPrice := 0.7, 0.9
	videoPricing := service.ChannelModelPricing{
		Platform: service.PlatformSeedance, Models: []string{"custom-video"}, BillingMode: service.BillingModeVideo,
		PerRequestPrice: &defaultPrice, VideoPricingVersion: service.VideoPricingVersion,
		VideoBillingUnit: service.VideoBillingPerVideo, VideoQuantityPolicy: service.VideoQuantityOutputSeconds,
		Intervals: []service.PricingInterval{{TierLabel: "1080p", PerRequestPrice: &resolutionPrice, SortOrder: 2}},
	}
	channel := &service.Channel{
		ModelPricing: []service.ChannelModelPricing{videoPricing},
		AccountStatsPricingRules: []service.AccountStatsPricingRule{{
			Name: "video costs", GroupIDs: []int64{}, AccountIDs: []int64{},
			Pricing: []service.ChannelModelPricing{videoPricing},
		}},
	}
	repo := newReasoningPricingIntegrationChannel(t, channel)
	assertPricing := func(pricing []service.ChannelModelPricing, wantResolution bool) {
		t.Helper()
		require.Len(t, pricing, 1)
		require.Equal(t, service.VideoBillingPerVideo, pricing[0].VideoBillingUnit)
		require.Equal(t, defaultPrice, *pricing[0].PerRequestPrice)
		if !wantResolution {
			require.Empty(t, pricing[0].Intervals)
			return
		}
		require.Len(t, pricing[0].Intervals, 1)
		interval := pricing[0].Intervals[0]
		require.Equal(t, "1080p", interval.TierLabel)
		require.Equal(t, resolutionPrice, *interval.PerRequestPrice)
		require.Equal(t, pricing[0].ID, interval.PricingID)
		require.NotZero(t, interval.ID)
		require.False(t, interval.CreatedAt.IsZero())
		require.False(t, interval.UpdatedAt.IsZero())
	}
	listed, err := repo.ListModelPricing(ctx, channel.ID)
	require.NoError(t, err)
	assertPricing(listed, true)
	batch, err := repo.batchLoadModelPricing(ctx, []int64{channel.ID})
	require.NoError(t, err)
	assertPricing(batch[channel.ID], true)
	loaded, err := repo.GetByID(ctx, channel.ID)
	require.NoError(t, err)
	require.Len(t, loaded.AccountStatsPricingRules, 1)
	assertPricing(loaded.AccountStatsPricingRules[0].Pricing, true)

	// Replacing a video row must read the replacement's own complete snapshot,
	// including the legitimate empty resolution list rather than stale children.
	videoPricing.Intervals = nil
	require.NoError(t, repo.ReplaceModelPricing(ctx, channel.ID, []service.ChannelModelPricing{videoPricing}))
	listed, err = repo.ListModelPricing(ctx, channel.ID)
	require.NoError(t, err)
	assertPricing(listed, false)
}
