package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type fallbackInflightRepriceKey struct{}
type fallbackInflightReprice func(context.Context, *service.APIKey) (context.Context, error)

// Bind the original estimate input and cleanup to the request, including when
// its primary group is free/unpriced. The fallback is attempted once, before
// submitting any usage task; a late reservation must outlive that task too.
func withFallbackInflightReprice(ctx context.Context, billing *service.BillingCacheService, estimator inflightReservationEstimator, key *service.APIKey, req service.InflightEstimateRequest, done func()) (context.Context, func()) {
	if key == nil || key.FallbackGroupID == nil || billing == nil || estimator == nil || !billing.InflightReservationEnabled() {
		return ctx, done
	}
	current := service.InflightReservationFromContext(ctx)
	reprice := fallbackInflightReprice(func(targetCtx context.Context, target *service.APIKey) (context.Context, error) {
		estimate, priced := estimator.EstimateInflightReservation(targetCtx, target, req)
		if !priced && billing.InflightReservationFailClosedOnUnpriced() {
			return targetCtx, service.ErrInsufficientBalance
		}
		updated, err := billing.RepriceInflight(targetCtx, current, target.User, target.Group, estimate)
		if err != nil {
			return targetCtx, err
		}
		if current == nil && updated != nil {
			// The deferred handler cleanup reads this variable at exit, rather
			// than retaining the no-op returned for the primary group.
			done = updated.HandlerDone
		}
		current = updated
		return service.WithInflightReservation(targetCtx, updated), nil
	})
	return context.WithValue(ctx, fallbackInflightRepriceKey{}, reprice), func() { done() }
}

func repriceFallbackInflight(ctx context.Context, key *service.APIKey) (context.Context, error) {
	if reprice, ok := ctx.Value(fallbackInflightRepriceKey{}).(fallbackInflightReprice); ok {
		return reprice(ctx, key)
	}
	return ctx, nil
}
