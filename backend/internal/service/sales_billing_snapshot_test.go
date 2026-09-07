package service

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSalesBillingSnapshotUsesAccountPricingWithoutChangingCharge(t *testing.T) {
	customCost, accountRate := 4.0, 0.25
	at := time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)
	p := &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: 10, ActualCost: 3},
		User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 3},
		AccountRateMultiplier: 0.5,
	}
	usage := &UsageLog{AccountStatsCost: &customCost, AccountRateMultiplier: &accountRate, CreatedAt: at}
	cmd := buildUsageBillingCommand("request", usage, p)
	require.Equal(t, 3.0, cmd.BalanceCost)
	require.NotNil(t, cmd.SalesCost)
	require.Equal(t, 1.0, *cmd.SalesCost)
	require.Equal(t, at, cmd.SalesConsumedAt)

	// Commission-only metadata cannot alter the legacy retry key.
	customCost = 8
	changed := buildUsageBillingCommand("request", usage, p)
	require.Equal(t, cmd.RequestFingerprint, changed.RequestFingerprint)
	require.Equal(t, 2.0, *changed.SalesCost)
}

func TestSalesBillingSnapshotDefaultCostAndInvalidCost(t *testing.T) {
	p := &postUsageBillingParams{Cost: &CostBreakdown{TotalCost: 2.5}, AccountRateMultiplier: 0.3}
	cmd := &UsageBillingCommand{}
	captureSalesBillingCost(cmd, nil, p)
	require.NotNil(t, cmd.SalesCost)
	require.Equal(t, 0.75, *cmd.SalesCost)
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		cmd = &UsageBillingCommand{}
		p.AccountRateMultiplier = invalid
		captureSalesBillingCost(cmd, nil, p)
		require.Nil(t, cmd.SalesCost)
	}
}

func TestSalesBillingSnapshotExcludesSubscriptions(t *testing.T) {
	cmd := &UsageBillingCommand{}
	captureSalesBillingCost(cmd, &UsageLog{}, &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: 2, ActualCost: 1}, IsSubscriptionBill: true,
	})
	require.Nil(t, cmd.SalesCost)
	require.True(t, cmd.SalesConsumedAt.IsZero())
}
