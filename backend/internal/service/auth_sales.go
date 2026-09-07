package service

import (
	"context"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// SalesRegistrationService keeps sales attribution out of the existing auth
// constructor and lets registration reuse the same database transaction.
type SalesRegistrationService interface {
	ResolveReferral(context.Context, string) (*SalesAttribution, error)
	BindCustomer(context.Context, int64, *SalesAttribution) error
}

type salesReferralContextKey struct{}

// ContextWithSalesReferral is populated only from a signed, HttpOnly referral
// cookie or a server-owned pending OAuth session, never a registration body.
func ContextWithSalesReferral(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, salesReferralContextKey{}, strings.TrimSpace(value))
}

func SalesReferralFromContext(ctx context.Context) string {
	value, _ := ctx.Value(salesReferralContextKey{}).(string)
	return value
}

func (s *AuthService) SetSalesService(sales SalesRegistrationService) {
	s.salesService = sales
}

// createUserWithSales binds only after this request successfully creates a new
// user. Existing-account login and OAuth identity adoption never call it.
func (s *AuthService) createUserWithSales(ctx context.Context, user *User, create func(context.Context, *User) error) error {
	referral := SalesReferralFromContext(ctx)
	if s.salesService == nil || referral == "" {
		return create(ctx, user)
	}
	// ResolveReferral also accepts explicit promotion codes for the public visit
	// endpoint. Auth only accepts the signed form; a bare cookie cannot select an
	// arbitrary salesperson.
	if strings.Count(referral, ".") != 1 || len(referral) > 1024 {
		return create(ctx, user)
	}
	attribution, err := s.salesService.ResolveReferral(ctx, referral)
	if IsSalesAttributionUnavailable(err) {
		return create(ctx, user)
	}
	if err != nil {
		return err
	}
	if attribution == nil {
		return create(ctx, user)
	}
	commitUser := func(txCtx context.Context) error {
		if err := create(txCtx, user); err != nil {
			return err
		}
		return s.salesService.BindCustomer(txCtx, user.ID, attribution)
	}
	if dbent.TxFromContext(ctx) != nil {
		return commitUser(ctx)
	}
	// A valid sales referral must not silently degrade into a non-atomic signup.
	if s.entClient == nil {
		return ErrServiceUnavailable
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := commitUser(dbent.NewTxContext(ctx, tx)); err != nil {
		return err
	}
	return tx.Commit()
}
