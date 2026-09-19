package service

import "context"

// CheckFallbackBillingEligibility rechecks the destination of a request whose
// user RPM admission has already succeeded. The destination has its own RPM
// limit and user override; neither the primary override nor a second user RPM
// increment may be carried across the group boundary.
func (s *BillingCacheService) CheckFallbackBillingEligibility(ctx context.Context, user *User, apiKey *APIKey, group *Group, platform string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || user == nil || group == nil || group.IsSubscriptionType() {
		return ErrBillingServiceUnavailable
	}
	fallbackUser := *user
	fallbackUser.RPMLimit = 0
	fallbackUser.UserGroupRPMOverride = nil
	return s.CheckBillingEligibility(ctx, &fallbackUser, apiKey, group, nil, platform)
}
