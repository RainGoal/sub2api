package service

import (
	"context"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// RepriceInflight updates the reservation before the one permitted API-key group
// fallback. The handle and billing-task references remain stable. An initially
// free/unpriced request may acquire its first reservation here.
func (s *BillingCacheService) RepriceInflight(ctx context.Context, current *InflightReservation, user *User, group *Group, estimate float64) (*InflightReservation, error) {
	if err := ctx.Err(); err != nil {
		return current, err
	}
	if current == nil {
		return s.ReserveInflight(ctx, user, group, nil, estimate)
	}
	cfg, enabled := s.inflightReservationConfig()
	if !enabled {
		return current, nil
	}
	if user == nil || current.userID != user.ID {
		return current, ErrBillingServiceUnavailable
	}
	if estimate < 0 || math.IsNaN(estimate) || math.IsInf(estimate, 0) {
		estimate = 0
	}
	if cfg.MaxReservationUSD > 0 && estimate > cfg.MaxReservationUSD {
		estimate = cfg.MaxReservationUSD
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.refs.Load() <= 0 {
		return current, ErrBillingServiceUnavailable
	}
	// Removing a reservation for a free destination needs no balance lookup.
	var balance float64
	if estimate > 0 {
		var err error
		balance, err = s.GetUserBalance(ctx, user.ID)
		if err != nil {
			logger.LegacyPrintf("service.billing_cache", "Warning: inflight reprice balance read failed for user %d (keeping prior reservation): %v", user.ID, err)
			return current, nil
		}
	}
	repriceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inflightReservationReserveTimeout)
	defer cancel()
	allowed, _, err := current.cache.ReserveInflightBalance(repriceCtx, user.ID, current.requestID, estimate, balance, current.ttl)
	if err != nil {
		// Preserve the existing fail-open policy and its original cleanup handle.
		logger.LegacyPrintf("service.billing_cache", "Warning: inflight reprice failed for user %d (keeping prior reservation): %v", user.ID, err)
		return current, nil
	}
	if !allowed {
		return current, ErrInsufficientBalance
	}
	current.amount = estimate
	return current, nil
}
