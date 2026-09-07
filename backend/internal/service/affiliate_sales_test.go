//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type affiliateSalesTestRepo struct {
	AffiliateRepository
	invitee       AffiliateSummary
	inviter       AffiliateSummary
	ensureCalls   int
	bindCalls     int
	accrueCalls   int
	accruedAmount float64
	orderID       *int64
}

func (r *affiliateSalesTestRepo) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	r.ensureCalls++
	if userID == r.invitee.UserID {
		return &r.invitee, nil
	}
	return &r.inviter, nil
}
func (r *affiliateSalesTestRepo) GetAffiliateByCode(_ context.Context, code string) (*AffiliateSummary, error) {
	if code != "REF-7" {
		return nil, ErrAffiliateProfileNotFound
	}
	return &r.inviter, nil
}
func (r *affiliateSalesTestRepo) BindInviter(_ context.Context, userID, inviterID int64) (bool, error) {
	r.bindCalls++
	if userID != r.invitee.UserID || r.invitee.InviterID != nil {
		return false, nil
	}
	r.invitee.InviterID = &inviterID
	return true, nil
}
func (r *affiliateSalesTestRepo) AccrueQuota(_ context.Context, _, _ int64, amount float64, _ int, orderID *int64) (bool, error) {
	r.accrueCalls++
	r.accruedAmount += amount
	r.orderID = orderID
	return true, nil
}

type affiliateSalesAwareTestRepo struct {
	*affiliateSalesTestRepo
	isCustomer    bool
	lookupErr     error
	checkedUserID int64
}

func (r *affiliateSalesAwareTestRepo) IsSalesCustomer(_ context.Context, userID int64) (bool, error) {
	r.checkedUserID = userID
	return r.isCustomer, r.lookupErr
}

func newAffiliateSalesTestService(repo AffiliateRepository) *AffiliateService {
	settings := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyAffiliateEnabled: "true", SettingKeyAffiliateRebateRate: "20",
		SettingKeyAffiliateRebateDurationDays: "0", SettingKeyAffiliateRebatePerInviteeCap: "0",
		// Existing sales ownership remains exclusive when new promotion is off.
		SalesSettingKey: `{"enabled":false}`,
	}}, nil)
	return NewAffiliateService(repo, settings, nil, nil)
}

func newAffiliateSalesTestRepo() *affiliateSalesTestRepo {
	rate := 25.0
	return &affiliateSalesTestRepo{
		invitee: AffiliateSummary{UserID: 101},
		inviter: AffiliateSummary{UserID: 7, AffCode: "REF-7", AffRebateRatePercent: &rate},
	}
}

func TestAffiliateSalesCustomerDoesNotBindOrdinaryInviter(t *testing.T) {
	repo := &affiliateSalesAwareTestRepo{affiliateSalesTestRepo: newAffiliateSalesTestRepo(), isCustomer: true}
	svc := newAffiliateSalesTestService(repo)
	require.NoError(t, svc.BindInviterByCode(context.Background(), 101, "ref-7"))
	require.EqualValues(t, 101, repo.checkedUserID)
	require.Nil(t, repo.invitee.InviterID)
	require.Zero(t, repo.bindCalls)
	require.Zero(t, repo.ensureCalls)
}

func TestAffiliateSalesCustomerDoesNotAccrueRechargeRebate(t *testing.T) {
	for _, paymentOrder := range []bool{false, true} {
		t.Run(map[bool]string{false: "admin-or-redeem", true: "payment-order"}[paymentOrder], func(t *testing.T) {
			repo := &affiliateSalesAwareTestRepo{affiliateSalesTestRepo: newAffiliateSalesTestRepo(), isCustomer: true}
			repo.invitee.InviterID = &repo.inviter.UserID // Even an old ordinary invitation cannot stack rewards.
			svc := newAffiliateSalesTestService(repo)
			var amount float64
			var err error
			if paymentOrder {
				id := int64(99)
				amount, err = svc.AccrueInviteRebateForOrder(context.Background(), 101, 100, &id)
			} else {
				amount, err = svc.AccrueInviteRebate(context.Background(), 101, 100)
			}
			require.NoError(t, err)
			require.Zero(t, amount)
			require.EqualValues(t, 101, repo.checkedUserID)
			require.Zero(t, repo.accrueCalls)
			require.Zero(t, repo.ensureCalls)
		})
	}
}

func TestAffiliateOrdinaryCustomerRetainsBindingAndRebate(t *testing.T) {
	for _, ownershipReader := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-repository", true: "ownership-aware-repository"}[ownershipReader], func(t *testing.T) {
			base := newAffiliateSalesTestRepo()
			var repo AffiliateRepository = base
			if ownershipReader {
				repo = &affiliateSalesAwareTestRepo{affiliateSalesTestRepo: base}
			}
			svc := newAffiliateSalesTestService(repo)
			require.NoError(t, svc.BindInviterByCode(context.Background(), 101, " ref-7 "))
			require.EqualValues(t, 7, *base.invitee.InviterID)
			require.Equal(t, 1, base.bindCalls)
			id := int64(99)
			amount, err := svc.AccrueInviteRebateForOrder(context.Background(), 101, 100, &id)
			require.NoError(t, err)
			require.Equal(t, 25.0, amount) // Existing personal 25% rate still overrides the global 20%.
			require.Equal(t, 25.0, base.accruedAmount)
			require.Equal(t, 1, base.accrueCalls)
			require.Equal(t, &id, base.orderID)
		})
	}
}

func TestAffiliateSalesOwnershipLookupFailureDoesNotIssueRewards(t *testing.T) {
	lookupErr := errors.New("ownership database unavailable")
	repo := &affiliateSalesAwareTestRepo{affiliateSalesTestRepo: newAffiliateSalesTestRepo(), lookupErr: lookupErr}
	svc := newAffiliateSalesTestService(repo)
	require.ErrorIs(t, svc.BindInviterByCode(context.Background(), 101, "REF-7"), lookupErr)
	amount, err := svc.AccrueInviteRebateForOrder(context.Background(), 101, 100, nil)
	require.ErrorIs(t, err, lookupErr)
	require.Zero(t, amount)
	require.Zero(t, repo.bindCalls)
	require.Zero(t, repo.accrueCalls)
	require.Zero(t, repo.ensureCalls)
}
