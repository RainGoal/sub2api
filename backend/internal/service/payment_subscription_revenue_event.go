package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func paymentFXRateToUSD(currency string, subscriptionUSDToCNYRate float64) *float64 {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USD":
		rate := 1.0
		return &rate
	case "CNY":
		rate := normalizeSubscriptionUSDToCNYRate(subscriptionUSDToCNYRate)
		if rate > 0 {
			usdRate := 1 / rate
			return &usdRate
		}
	}
	return nil
}

func snapshotSubscriptionPaymentFX(snapshot map[string]any, currency string, subscriptionUSDToCNYRate float64) {
	if snapshot == nil {
		return
	}
	if rate := paymentFXRateToUSD(currency, subscriptionUSDToCNYRate); rate != nil {
		snapshot["payment_fx_rate_to_usd"] = *rate
	}
}

func subscriptionPaymentFXRate(order *dbent.PaymentOrder) *float64 {
	if order == nil || len(order.ProviderSnapshot) == 0 {
		return nil
	}
	value, ok := order.ProviderSnapshot["payment_fx_rate_to_usd"]
	if !ok {
		return nil
	}
	var rate float64
	switch typed := value.(type) {
	case float64:
		rate = typed
	case float32:
		rate = float64(typed)
	case int:
		rate = float64(typed)
	case int64:
		rate = float64(typed)
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return nil
		}
		rate = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return nil
		}
		rate = parsed
	default:
		return nil
	}
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return nil
	}
	return &rate
}

func insertSubscriptionRevenueEvent(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	subscription *UserSubscription,
	serviceStartAt time.Time,
	serviceEndAt time.Time,
) error {
	if client == nil || order == nil || subscription == nil {
		return fmt.Errorf("subscription revenue event requires client, order, and subscription")
	}
	if serviceStartAt.IsZero() || !serviceEndAt.After(serviceStartAt) {
		return fmt.Errorf("subscription revenue event has invalid service period")
	}
	if order.OrderType != payment.OrderTypeSubscription || order.Amount < 0 || order.PayAmount < 0 {
		return fmt.Errorf("subscription revenue event has invalid order amounts")
	}
	var planID any
	if order.PlanID != nil {
		planID = *order.PlanID
	}
	currency := PaymentOrderCurrency(order)
	fxRate := subscriptionPaymentFXRate(order)
	var fxArg any
	if fxRate != nil {
		fxArg = *fxRate
	}
	_, err := client.ExecContext(ctx, `
		INSERT INTO subscription_revenue_events (
			order_id, subscription_id, user_id, group_id, plan_id,
			service_start_at, service_end_at, order_amount, paid_amount,
			payment_currency, payment_fx_rate_to_usd
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (order_id) DO NOTHING
	`, order.ID, subscription.ID, order.UserID, subscription.GroupID, planID,
		serviceStartAt, serviceEndAt, order.Amount, order.PayAmount, currency, fxArg)
	if err != nil {
		return fmt.Errorf("insert event row: %w", err)
	}
	return nil
}
