package repository

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newVideoAssetUploadLimiterTest(t *testing.T) (*videoAssetUploadLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter, ok := NewVideoAssetUploadLimiter(client).(*videoAssetUploadLimiter)
	require.True(t, ok)
	return limiter, mr
}

func TestVideoAssetUploadLimiterRateLimit(t *testing.T) {
	limiter, _ := newVideoAssetUploadLimiterTest(t)
	ctx := context.Background()

	allowed, _, err := limiter.Allow(ctx, 42, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, _, err = limiter.Allow(ctx, 42, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, retryAfter, err := limiter.Allow(ctx, 42, 2)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Positive(t, retryAfter)
}

func TestVideoAssetUploadLimiterRateLimitChangesPreserveCurrentWindow(t *testing.T) {
	limiter, _ := newVideoAssetUploadLimiterTest(t)
	ctx := context.Background()

	for range 2 {
		allowed, _, err := limiter.Allow(ctx, 43, 10)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	allowed, _, err := limiter.Allow(ctx, 43, 1)
	require.NoError(t, err)
	require.False(t, allowed, "changing the limit must retain the current window count")
}

func TestVideoAssetUploadLimiterDailyByteLimitChangesPreserveUsage(t *testing.T) {
	limiter, _ := newVideoAssetUploadLimiterTest(t)
	ctx := context.Background()

	allowed, err := limiter.ReserveBytes(ctx, 44, 60, 100)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = limiter.ReserveBytes(ctx, 44, 1, 50)
	require.NoError(t, err)
	require.False(t, allowed, "lowering the limit must retain bytes already reserved")
	allowed, err = limiter.ReserveBytes(ctx, 44, 30, 100)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = limiter.ReserveBytes(ctx, 44, 11, 100)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestVideoAssetUploadLimiterRedisFailure(t *testing.T) {
	limiter, mr := newVideoAssetUploadLimiterTest(t)
	mr.Close()

	allowed, _, err := limiter.Allow(context.Background(), 45, 1)
	require.Error(t, err)
	require.False(t, allowed)
	allowed, err = limiter.ReserveBytes(context.Background(), 45, 1, 10)
	require.Error(t, err)
	require.False(t, allowed)
}
