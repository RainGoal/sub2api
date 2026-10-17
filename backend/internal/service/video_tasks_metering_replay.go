package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrVideoTaskMeteringUnavailable = errors.New("frozen video task metering is unavailable")

// BuildSeedanceVideoCompletionBillingWithMetering reuses the first settlement's
// quantities before validating a later provider response or calculating costs.
func (s *OpenAIGatewayService) BuildSeedanceVideoCompletionBillingWithMetering(ctx context.Context, pending *SeedanceVideoPendingBilling, status *OpenAIForwardResult) (*OpenAIForwardResult, *CostBreakdown, error) {
	result, cost, err := s.frozenVideoTaskCompletion(ctx, pending, status)
	if err != nil || result != nil {
		return result, cost, err
	}
	return BuildSeedanceVideoCompletionBilling(pending, status)
}

func (s *OpenAIGatewayService) frozenVideoTaskCompletion(ctx context.Context, pending *SeedanceVideoPendingBilling, status *OpenAIForwardResult) (*OpenAIForwardResult, *CostBreakdown, error) {
	if pending == nil || pending.SalesSnapshot == nil {
		return nil, nil, nil
	}
	repo, ok := s.seedanceVideoTaskRepo.(VideoTaskMeteringRepository)
	if !ok {
		return nil, nil, ErrVideoTaskMeteringUnavailable
	}
	payload, err := repo.GetVideoTaskMetering(ctx, pending.StateID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrVideoTaskMeteringUnavailable, err)
	}
	if len(payload) == 0 {
		return nil, nil, nil
	}
	var snapshot videoTaskMeteringSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, nil, fmt.Errorf("%w: invalid snapshot: %v", ErrVideoTaskMeteringUnavailable, err)
	}
	if snapshot.Version != VideoPricingVersion || snapshot.RequestedSeconds != pending.DurationSeconds {
		return nil, nil, fmt.Errorf("%w: invalid version or requested duration", ErrVideoTaskMeteringUnavailable)
	}
	for _, value := range []float64{snapshot.OutputSeconds, snapshot.ReferenceSeconds, snapshot.SalesQuantity, snapshot.PurchaseQuantity, snapshot.SalesCost} {
		if !validSeedanceAccountCost(value) {
			return nil, nil, fmt.Errorf("%w: invalid quantity or cost", ErrVideoTaskMeteringUnavailable)
		}
	}
	if snapshot.PurchaseCost != nil && !validSeedanceAccountCost(*snapshot.PurchaseCost) {
		return nil, nil, fmt.Errorf("%w: invalid purchase cost", ErrVideoTaskMeteringUnavailable)
	}
	if status == nil {
		return nil, nil, fmt.Errorf("video completion settlement input is unavailable")
	}
	replayed := *status
	replayed.VideoDurationSecondsExact, replayed.VideoReferenceInputSecondsExact = snapshot.OutputSeconds, snapshot.ReferenceSeconds
	replayed.VideoDurationSeconds, replayed.VideoReferenceInputSeconds = int(snapshot.OutputSeconds), int(snapshot.ReferenceSeconds)
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &replayed)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrVideoTaskMeteringUnavailable, err)
	}
	quantity := func(unit VideoBillingUnit, policy VideoQuantityPolicy, maximum float64) (float64, error) {
		output := snapshot.OutputSeconds
		if policy == VideoQuantityRequestedSeconds {
			output = float64(snapshot.RequestedSeconds)
		}
		return VideoBillingQuantity(unit, policy, maximum, output, snapshot.ReferenceSeconds)
	}
	salesQuantity, err := quantity(pending.SalesSnapshot.BillingUnit, pending.SalesSnapshot.QuantityPolicy, pending.SalesSnapshot.MaxBillableSeconds)
	if err != nil || salesQuantity != snapshot.SalesQuantity || cost.ActualCost != snapshot.SalesCost {
		return nil, nil, fmt.Errorf("%w: sales terms do not match frozen metering", ErrVideoTaskMeteringUnavailable)
	}
	purchaseQuantity := float64(0)
	if pending.AccountCost != nil && pending.AccountCost.Version == VideoPricingVersion {
		purchaseQuantity, err = quantity(pending.AccountCost.BillingUnit, pending.AccountCost.QuantityPolicy, pending.AccountCost.MaxBillableSeconds)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrVideoTaskMeteringUnavailable, err)
		}
	}
	input := &OpenAIRecordUsageInput{Result: result, CostOverride: cost}
	if err := ApplySeedanceAccountCost(input, pending); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrVideoTaskMeteringUnavailable, err)
	}
	if purchaseQuantity != snapshot.PurchaseQuantity || (input.AccountStatsCostOverride == nil) != (snapshot.PurchaseCost == nil) {
		return nil, nil, fmt.Errorf("%w: purchase terms do not match frozen metering", ErrVideoTaskMeteringUnavailable)
	}
	if snapshot.PurchaseCost != nil && *input.AccountStatsCostOverride**input.AccountStatsRateMultiplierOverride != *snapshot.PurchaseCost {
		return nil, nil, fmt.Errorf("%w: purchase cost does not match frozen metering", ErrVideoTaskMeteringUnavailable)
	}
	return result, cost, nil
}
