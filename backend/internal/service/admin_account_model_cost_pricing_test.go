//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func storageModelCostPrices(price float64) []any {
	return []any{map[string]any{
		"models": []any{"vendor-model"}, "billing_mode": "per_request", "per_request_price": price,
	}}
}

type accountCostPricingWriteRepo struct {
	longContextBillingRepoStub
	writtenExtra map[string]any
}

func (r *accountCostPricingWriteRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.writtenExtra = updates
	return r.longContextBillingRepoStub.UpdateExtra(ctx, id, updates)
}

func (r *accountCostPricingWriteRepo) BulkUpdate(ctx context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.writtenExtra = updates.Extra
	return r.longContextBillingRepoStub.BulkUpdate(ctx, ids, updates)
}

func TestAdminUpdateAccountModelCostPricingDistinguishesOmissionAndClear(t *testing.T) {
	for _, tt := range []struct {
		name     string
		extra    map[string]any
		provided bool
	}{
		{"nil extra preserves database value", nil, false},
		{"empty extra preserves database value", map[string]any{}, false},
		{"unrelated edit preserves database value", map[string]any{"quota_limit": 100}, false},
		{"empty array explicitly clears", map[string]any{AccountModelCostPricingExtraKey: []any{}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{AccountModelCostPricingExtraKey: storageModelCostPrices(0.03)}}
			repo := &longContextBillingRepoStub{account: original}
			svc := &adminServiceImpl{accountRepo: repo}
			_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: tt.extra})
			require.NoError(t, err)
			if tt.provided {
				require.Contains(t, repo.account.Extra, AccountModelCostPricingExtraKey)
				require.Empty(t, repo.account.Extra[AccountModelCostPricingExtraKey])
			} else {
				// The repository merges an omitted key from the current locked row;
				// the stale value loaded by the service must not become an update.
				require.NotContains(t, repo.account.Extra, AccountModelCostPricingExtraKey)
			}
		})
	}
}

func TestAdminAccountModelCostPricingRejectsMalformedUpdates(t *testing.T) {
	for _, path := range []string{"update", "extra", "bulk"} {
		t.Run(path, func(t *testing.T) {
			repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
			svc := &adminServiceImpl{accountRepo: repo}
			extra := map[string]any{AccountModelCostPricingExtraKey: nil}
			var err error
			switch path {
			case "update":
				_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: extra})
			case "extra":
				err = svc.UpdateAccountExtra(context.Background(), 1, extra)
			case "bulk":
				_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
			}
			require.Error(t, err)
			require.Zero(t, repo.updateExtraCalls)
			require.Zero(t, repo.bulkUpdateCalls)
			require.Nil(t, repo.account.Extra)
		})
	}
}

func TestAdminBulkAccountModelCostPricingValidatesEveryTarget(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{
		{ID: 1, Platform: PlatformOpenAI}, {ID: 2, Platform: PlatformAnthropic},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2}, Extra: map[string]any{AccountModelCostPricingExtraKey: storageModelCostPrices(0.03)},
	})
	require.Error(t, err, "one platform's normalized pricing cannot be assigned to another platform")
	require.Zero(t, repo.bulkUpdateCalls)

	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2}, Extra: map[string]any{AccountModelCostPricingExtraKey: []any{}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.bulkUpdateCalls)
	require.Contains(t, repo.lastBulkUpdate.Extra, AccountModelCostPricingExtraKey)
	require.Empty(t, repo.lastBulkUpdate.Extra[AccountModelCostPricingExtraKey])
}

func TestAdminAccountModelCostPricingNormalizesUpdatePaths(t *testing.T) {
	for _, path := range []string{"update", "extra", "bulk"} {
		t.Run(path, func(t *testing.T) {
			repo := &accountCostPricingWriteRepo{longContextBillingRepoStub: longContextBillingRepoStub{
				account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			}}
			svc := &adminServiceImpl{accountRepo: repo}
			extra := map[string]any{AccountModelCostPricingExtraKey: storageModelCostPrices(0.03)}
			var err error
			switch path {
			case "update":
				_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: extra})
			case "extra":
				err = svc.UpdateAccountExtra(context.Background(), 1, extra)
			case "bulk":
				_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
			}
			require.NoError(t, err)
			written := repo.writtenExtra
			if path == "update" {
				written = repo.account.Extra
			}
			prices, ok := written[AccountModelCostPricingExtraKey].([]ChannelModelPricing)
			require.True(t, ok)
			require.Len(t, prices, 1)
			require.Equal(t, PlatformOpenAI, prices[0].Platform)
		})
	}
}

func TestDuplicateAccountModelCostPricingIsCopiedAndValidated(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	source := &Account{
		Name: "priced", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test"},
		Extra:       map[string]any{AccountModelCostPricingExtraKey: storageModelCostPrices(0.03)},
	}
	require.NoError(t, repo.Create(ctx, source))
	duplicate, err := svc.DuplicateAccount(ctx, source.ID, "admin:1", "")
	require.NoError(t, err)
	prices, ok := duplicate.Extra[AccountModelCostPricingExtraKey].([]ChannelModelPricing)
	require.True(t, ok)
	require.Len(t, prices, 1)
	require.Equal(t, 0.03, *prices[0].PerRequestPrice)
	*prices[0].PerRequestPrice = 0.05
	require.Equal(t, 0.03, source.Extra[AccountModelCostPricingExtraKey].([]any)[0].(map[string]any)["per_request_price"])

	source.Extra[AccountModelCostPricingExtraKey] = nil
	countBefore := len(repo.accounts)
	_, err = svc.DuplicateAccount(ctx, source.ID, "admin:1", "")
	require.Error(t, err)
	require.Len(t, repo.accounts, countBefore)
}

func TestAdminCreateAccountModelCostPricing(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  any
		fail bool
	}{
		{"configured", storageModelCostPrices(0.03), false},
		{"explicitly empty", []any{}, false},
		{"null is invalid", nil, true},
		{"object is invalid", map[string]any{}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &longContextBillingRepoStub{}
			svc := &adminServiceImpl{accountRepo: repo}
			account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
				Name: "priced", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test"}, SkipDefaultGroupBind: true,
				Extra: map[string]any{AccountModelCostPricingExtraKey: tt.raw},
			})
			if tt.fail {
				require.Error(t, err)
				require.Nil(t, repo.createdAccount)
				return
			}
			require.NoError(t, err)
			prices, ok := account.Extra[AccountModelCostPricingExtraKey].([]ChannelModelPricing)
			require.True(t, ok)
			if len(prices) > 0 {
				require.Equal(t, PlatformOpenAI, prices[0].Platform)
				require.Equal(t, 0.03, *prices[0].PerRequestPrice)
			} else {
				require.NotNil(t, prices)
			}
		})
	}
}
