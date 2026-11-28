//go:build unit

package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type publicPlansSettings struct {
	service.SettingRepository
	initialEnabled string
	values         map[string]string
}

func (s publicPlansSettings) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingPaymentEnabled {
		return s.initialEnabled, nil
	}
	return s.values[key], nil
}

func (s publicPlansSettings) GetMultiple(_ context.Context, _ []string) (map[string]string, error) {
	return s.values, nil
}

func requestPublicPlans(t *testing.T, cfg *service.PaymentConfigService) string {
	t.Helper()
	r := gin.New()
	r.GET("/api/v1/payment/public/plans", NewPaymentHandler(nil, cfg).GetPublicPlans)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/payment/public/plans", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return w.Body.String()
}

func TestGetPublicPlansReturnsEmptyWhilePaymentDisabled(t *testing.T) {
	tests := []struct {
		name           string
		initialEnabled string
	}{
		{name: "disabled at start", initialEnabled: "false"},
		{name: "disabled on config read", initialEnabled: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := service.NewPaymentConfigService(nil, publicPlansSettings{
				initialEnabled: tt.initialEnabled,
				values: map[string]string{
					service.SettingPaymentEnabled:           "false",
					service.SettingSubscriptionUSDToCNYRate: "7.2",
					service.SettingRechargeBonusMode:        "discount",
					service.SettingRechargeBonusTiers:       `[{"min_amount":10,"bonus_percent":5}]`,
				},
			}, nil)
			require.JSONEq(t, `{"code":0,"message":"success","data":{"plans":[],"subscription_usd_to_cny_rate":0,"has_cny_payment_method":false,"recharge_bonus_tiers":[],"recharge_bonus_mode":"bonus"}}`, requestPublicPlans(t, cfg))
		})
	}
}

func TestGetPublicPlansFiltersAndReturnsOnlyPublicFields(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:payment_public_plans?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	group, err := client.Group.Create().SetName("Internal group").SetPlatform("openai").
		SetDailyLimitUsd(20).SetMonthlyLimitUsd(300).Save(ctx)
	require.NoError(t, err)

	createPlan := func(name string, price float64, public, forSale bool, sortOrder int) *dbent.SubscriptionPlanCreate {
		return client.SubscriptionPlan.Create().SetGroupID(group.ID).SetName(name).SetPrice(price).
			SetIsPublic(public).SetForSale(forSale).SetSortOrder(sortOrder).SetProductName("Internal product")
	}
	firstTie := createPlan("First tie", 30, true, true, 4).SaveX(ctx)
	secondTie := createPlan("Second tie", 50, true, true, 4).SaveX(ctx)
	first := createPlan("First", 15, true, true, 2).SetDescription("Public details").SetOriginalPrice(25).
		SetCurrency("USD").SetFeatures(" feature A \n\nfeature B").SaveX(ctx)
	createPlan("Private", 30, false, true, 1).SaveX(ctx)
	createPlan("Off sale", 30, true, false, 1).SaveX(ctx)
	createPlan("Free", 0, true, true, 1).SaveX(ctx)
	createPlan("Negative", -5, true, true, 1).SaveX(ctx)
	client.PaymentProviderInstance.Create().SetProviderKey(payment.TypeStripe).SetConfig(`{"currency":"USD","publishableKey":"pk_test_example"}`).
		SetSupportedTypes("card").SetEnabled(true).SaveX(ctx)
	client.PaymentProviderInstance.Create().SetProviderKey(payment.TypeAlipay).SetConfig(`{}`).
		SetSupportedTypes("alipay").SetEnabled(false).SaveX(ctx)

	cfg := service.NewPaymentConfigService(client, publicPlansSettings{
		initialEnabled: "true",
		values: map[string]string{
			service.SettingPaymentEnabled:           "true",
			service.SettingSubscriptionUSDToCNYRate: "7.2",
			service.SettingRechargeBonusMode:        "discount",
			service.SettingRechargeBonusTiers:       `[{"min_amount":10,"bonus_percent":5}]`,
			service.SettingHelpText:                 "Internal help text",
		},
	}, nil)
	expected := `{
		"code": 0, "message": "success", "data": {
			"plans": [
				{"id": %d, "name": "First", "description": "Public details", "group_platform": "openai", "price": 15, "original_price": 25, "currency": "USD", "validity_days": 30, "validity_unit": "day", "features": ["feature A", "feature B"], "daily_limit_usd": 20, "weekly_limit_usd": null, "monthly_limit_usd": 300, "sort_order": 2},
				{"id": %d, "name": "First tie", "description": "", "group_platform": "openai", "price": 30, "validity_days": 30, "validity_unit": "day", "features": [], "daily_limit_usd": 20, "weekly_limit_usd": null, "monthly_limit_usd": 300, "sort_order": 4},
				{"id": %d, "name": "Second tie", "description": "", "group_platform": "openai", "price": 50, "validity_days": 30, "validity_unit": "day", "features": [], "daily_limit_usd": 20, "weekly_limit_usd": null, "monthly_limit_usd": 300, "sort_order": 4}
			],
			"subscription_usd_to_cny_rate": 7.2, "has_cny_payment_method": %t,
			"recharge_bonus_tiers": [{"min_amount": 10, "bonus_percent": 5}], "recharge_bonus_mode": "discount"
		}
	}`
	require.JSONEq(t, fmt.Sprintf(expected, first.ID, firstTie.ID, secondTie.ID, false), requestPublicPlans(t, cfg))
	client.PaymentProviderInstance.Create().SetProviderKey(payment.TypeAlipay).SetConfig(`{}`).
		SetSupportedTypes("alipay").SetEnabled(true).SaveX(ctx)
	require.JSONEq(t, fmt.Sprintf(expected, first.ID, firstTie.ID, secondTie.ID, true), requestPublicPlans(t, cfg))
}
