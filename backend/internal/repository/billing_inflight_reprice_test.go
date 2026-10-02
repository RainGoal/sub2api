package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInflightRepriceAtomicReplacement(t *testing.T) {
	_, cache, _ := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	reserve := func(id string, amount, balance float64, want bool) {
		t.Helper()
		allowed, _, err := cache.ReserveInflightBalance(ctx, 1, id, amount, balance, time.Minute)
		require.NoError(t, err)
		require.Equal(t, want, allowed)
	}
	reserve("request", 0.01, 1.5, true)
	reserve("request", 2, 1.5, true) // Still the only request: preserve first-request admission.
	reserve("request", 1, 1.5, true)
	reserve("other", 0.4, 1.5, true)
	reserve("request", 1.1, 1.5, true) // Exclude this request's previous $1.
	reserve("request", 1.2, 1.5, false)
	_, hkey := billingInflightKeys(1)
	amount, err := cache.rdb.HGet(ctx, hkey, "request").Float64()
	require.NoError(t, err)
	require.Equal(t, 1.1, amount, "rejection must preserve the prior hold")
	reserve("request", 0.9, 0.1, true) // A balance change cannot block a reduction.
	reserve("request", 0, 0.1, true)
	require.EqualValues(t, 1, inflightCount(t, cache, 1))
	exists, err := cache.rdb.HExists(ctx, hkey, "request").Result()
	require.NoError(t, err)
	require.False(t, exists)
}

func TestInflightRepriceConcurrentIncreasesRespectBalance(t *testing.T) {
	_, cache, _ := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	const n = 12
	for i := range n {
		allowed, _, err := cache.ReserveInflightBalance(ctx, 1, string(rune('a'+i)), 0.01, 1.5, time.Minute)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			allowed, _, err := cache.ReserveInflightBalance(ctx, 1, string(rune('a'+i)), 1, 1.5, time.Minute)
			if err != nil {
				t.Errorf("reprice: %v", err)
			}
			if allowed {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	require.EqualValues(t, 1, admitted.Load())
	require.EqualValues(t, n, inflightCount(t, cache, 1), "updates must not add or lose members")
}
