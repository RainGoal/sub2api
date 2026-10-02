//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type failingRepriceCache struct {
	*memInflightCache
	fail bool
}

func (c *failingRepriceCache) ReserveInflightBalance(ctx context.Context, userID int64, id string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	if c.fail {
		return false, 0, errors.New("cache unavailable")
	}
	return c.memInflightCache.ReserveInflightBalance(ctx, userID, id, amount, balance, ttl)
}

func TestRepriceInflightFailureAndCancellationPreserveOriginal(t *testing.T) {
	cache := &failingRepriceCache{memInflightCache: newMemInflightCache(2)}
	svc := newInflightSvc(t, cache, 60)
	ctx := context.Background()
	user := &User{ID: 1}
	res, err := svc.ReserveInflight(ctx, user, nil, nil, 0.25)
	require.NoError(t, err)
	defer res.HandlerDone()
	cache.fail = true
	updated, err := svc.RepriceInflight(ctx, res, user, nil, 1)
	require.NoError(t, err, "cache errors preserve fail-open behavior")
	require.Same(t, res, updated)
	require.Equal(t, 0.25, res.Amount())
	require.Equal(t, 1, cache.count())
	cache.fail = false
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = svc.RepriceInflight(canceled, res, user, nil, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0.25, res.Amount())
	svc.cfg.Billing.InflightReservation.MaxReservationUSD = 0.75
	updated, err = svc.RepriceInflight(ctx, res, user, nil, 2)
	require.NoError(t, err)
	require.Same(t, res, updated)
	require.Equal(t, 0.75, res.Amount(), "repricing uses the configured cap")
	res.HandlerDone()
	_, err = svc.RepriceInflight(ctx, res, user, nil, 1)
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.Zero(t, cache.count(), "released handles must not recreate a hold")
}

func TestRepriceInflightAndReleaseCannotLeakReservation(t *testing.T) {
	cache := newMemInflightCache(2)
	svc := newInflightSvc(t, cache, 60)
	ctx := context.Background()
	user := &User{ID: 1}
	for range 30 {
		res, err := svc.ReserveInflight(ctx, user, nil, nil, 0.1)
		require.NoError(t, err)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = svc.RepriceInflight(ctx, res, user, nil, 1)
		}()
		go func() {
			defer wg.Done()
			_ = res.Amount()
			res.HandlerDone()
		}()
		wg.Wait()
		require.Zero(t, cache.count())
	}
}
