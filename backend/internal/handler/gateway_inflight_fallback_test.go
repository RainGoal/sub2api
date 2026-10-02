package handler

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyFallbackRepricesInflightAndPreservesBillingHandoff(t *testing.T) {
	cheap, expensive, free := 0.01, 1.0, 0.0
	for _, tc := range []struct {
		name            string
		primary, target *float64
		otherHold       float64
		blocked, closed bool
		prepareRejected bool
	}{
		{name: "increase", primary: &cheap, target: &expensive},
		{name: "decrease", primary: &expensive, target: &cheap},
		{name: "free primary", primary: &free, target: &expensive},
		{name: "unpriced primary", target: &expensive},
		{name: "free destination", primary: &expensive, target: &free},
		{name: "unpriced destination fail open", primary: &expensive},
		{name: "unpriced destination fail closed", primary: &cheap, closed: true, blocked: true},
		{name: "increase exceeds available balance", primary: &cheap, target: &expensive, otherHold: 1, blocked: true},
		{name: "late reservation exceeds available balance", primary: &free, target: &expensive, otherHold: 1, blocked: true},
		{name: "policy rejection keeps primary", primary: &cheap, target: &expensive, prepareRejected: true, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, key, fallback, resolver, _ := fallbackTestState(t)
			setPrice := func(group *service.Group, price *float64) {
				group.RateMultiplier = 1
				if price != nil {
					group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"custom-fallback"}, BillingMode: service.BillingModePerRequest, PerRequestPrice: price}}
				}
			}
			setPrice(key.Group, tc.primary)
			setPrice(resolver.group, tc.target)
			cache := newHandlerInflightCache(1.5)
			cfg := &config.Config{}
			cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60, FailClosedOnUnpriced: tc.closed}
			billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			fallback.billing = billing
			pricing := service.NewBillingService(cfg, nil)
			gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
				pricing, nil, billing, nil, nil, nil, nil, service.NewModelPricingResolver(nil, pricing), nil, nil, nil, nil)
			req := tokenInflightEstimate("custom-fallback", []byte(`{"max_tokens":1000}`))
			done, err := reserveInflightBalance(c, billing, gateway, key, nil, req)
			require.NoError(t, err)
			defer done()
			old := service.InflightReservationFromContext(c.Request.Context())
			other, err := billing.ReserveInflight(context.Background(), key.User, key.Group, nil, tc.otherHold)
			require.NoError(t, err)
			defer other.HandlerDone()
			originalRequest := c.Request
			switches := 0
			next, err := fallback.Try(c, key, req.Model, &switches, apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, func(*gin.Context, *service.APIKey) error {
				if tc.prepareRejected {
					return service.ErrGroupNotAllowed
				}
				return nil
			})
			if tc.blocked {
				require.Error(t, err)
				require.Nil(t, next)
				require.Same(t, originalRequest, c.Request)
				require.Zero(t, switches)
				if old != nil {
					require.InDelta(t, *tc.primary, old.Amount(), 1e-12)
				}
				done()
				other.HandlerDone()
				require.Zero(t, cache.count())
				return
			}
			require.NoError(t, err)
			require.NotNil(t, next)
			want := 0.0
			if tc.target != nil {
				want = *tc.target
			}
			updated := service.InflightReservationFromContext(c.Request.Context())
			require.InDelta(t, want, updated.Amount(), 1e-12)
			if old != nil {
				require.Same(t, old, updated, "repricing preserves the existing handle")
			}
			probe, probeErr := billing.ReserveInflight(context.Background(), key.User, next.Group, nil, 0.6)
			if want == expensive {
				require.ErrorIs(t, probeErr, service.ErrInsufficientBalance)
			} else {
				require.NoError(t, probeErr)
				probe.HandlerDone()
			}
			// A second primary request must see the target price held by the first.
			if tc.primary != nil && *tc.primary > 0 {
				done2, err2 := reserveInflightBalance(newInflightTestGinContext(), billing, gateway, key, nil, req)
				require.NoError(t, err2)
				done2()
			}
			task, abandon := wrapUsageRecordTaskContext(c.Request.Context(), func(ctx context.Context) {
				require.NoError(t, billing.DeductBalanceCache(ctx, key.User.ID, want))
			})
			done()
			if want > 0 {
				require.Equal(t, 1, cache.count())
				_, err = billing.ReserveInflight(context.Background(), key.User, next.Group, nil, 1.5)
				require.ErrorIs(t, err, service.ErrInsufficientBalance)
			}
			task(context.Background())
			abandon()
			done()
			require.Zero(t, cache.count())
			balance, err := cache.GetUserBalance(context.Background(), key.User.ID)
			require.NoError(t, err)
			require.InDelta(t, 1.5-want, balance, 1e-12)
		})
	}
}

type cancelAfterInflightCache struct {
	*handlerInflightCache
	cancel context.CancelFunc
}

func (m *cancelAfterInflightCache) ReserveInflightBalance(ctx context.Context, userID int64, id string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	allowed, sum, err := m.handlerInflightCache.ReserveInflightBalance(ctx, userID, id, amount, balance, ttl)
	if m.cancel != nil {
		m.cancel()
	}
	return allowed, sum, err
}

func TestAPIKeyFallbackCanceledAfterLateReservationReleasesHold(t *testing.T) {
	c, key, fallback, _, _ := fallbackTestState(t)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	cache := &cancelAfterInflightCache{handlerInflightCache: newHandlerInflightCache(2), cancel: cancel}
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	fallback.billing = billing
	estimator := &countingEstimator{priced: true}
	done, err := reserveInflightBalance(c, billing, estimator, key, nil, tokenInflightEstimate("custom", nil))
	require.NoError(t, err)
	defer done()
	require.Nil(t, service.InflightReservationFromContext(c.Request.Context()))
	original := c.Request
	estimator.cost = 1 // Only the destination needs a reservation.
	switches := 0
	next, err := fallback.Try(c, key, "custom", &switches, apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, nil)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Zero(t, switches)
	require.Same(t, original, c.Request)
	require.Equal(t, 1, cache.count(), "the abandoned trial acquired a hold before cancellation")
	done()
	require.Zero(t, cache.count(), "original handler cleanup must own the late hold")
}
