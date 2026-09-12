//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCacheModelCostPricingSurvivesSnapshotAndClear(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	prices := []any{map[string]any{
		"models": []any{"vendor-model"}, "billing_mode": "per_request", "per_request_price": 0.03,
	}}
	account := service.Account{
		ID: 193, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Extra: map[string]any{service.AccountModelCostPricingExtraKey: prices},
	}
	bucket := service.SchedulerBucket{Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
	for _, clear := range []bool{false, true} {
		if clear {
			account.Extra[service.AccountModelCostPricingExtraKey] = []any{}
			require.NoError(t, cache.SetAccount(ctx, &account))
		}
		accounts, hit, err := cache.GetSnapshot(ctx, bucket)
		require.NoError(t, err)
		require.True(t, hit)
		require.Len(t, accounts, 1)
		require.Contains(t, accounts[0].Extra, service.AccountModelCostPricingExtraKey)
		if clear {
			require.Empty(t, accounts[0].Extra[service.AccountModelCostPricingExtraKey])
		} else {
			require.Equal(t, prices, accounts[0].Extra[service.AccountModelCostPricingExtraKey])
		}
	}
}
