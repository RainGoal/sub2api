package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type VideoTaskMeteringRepository interface {
	GetVideoTaskMetering(context.Context, string) (json.RawMessage, error)
	SaveVideoTaskMeteringWithLease(context.Context, string, json.RawMessage, time.Time) error
}

type videoTaskMeteringSnapshot struct {
	Version          int      `json:"version"`
	OutputSeconds    float64  `json:"output_seconds"`
	ReferenceSeconds float64  `json:"reference_seconds"`
	RequestedSeconds int      `json:"requested_seconds"`
	SalesQuantity    float64  `json:"sales_quantity"`
	PurchaseQuantity float64  `json:"purchase_quantity"`
	SalesCost        float64  `json:"sales_cost"`
	PurchaseCost     *float64 `json:"purchase_cost"`
}

// PersistVideoTaskMetering retains exact quantities alongside immutable prices.
// Existing integer usage display columns remain compatible with old clients.
func (s *OpenAIGatewayService) PersistVideoTaskMetering(ctx context.Context, pending *SeedanceVideoPendingBilling, input *OpenAIRecordUsageInput) error {
	if pending == nil || pending.SalesSnapshot == nil {
		return nil
	}
	if input == nil || input.Result == nil || input.CostOverride == nil {
		return fmt.Errorf("video metering settlement input is unavailable")
	}
	repo, ok := s.seedanceVideoTaskRepo.(VideoTaskMeteringRepository)
	if !ok {
		return fmt.Errorf("video metering repository is unavailable")
	}
	lease, ok := seedanceVideoPendingLease(pending)
	if !ok {
		return ErrSeedanceVideoLeaseMutationUnsupported
	}
	output, reference := input.Result.VideoDurationSecondsExact, input.Result.VideoReferenceInputSecondsExact
	if pending.ExecutionSnapshot == nil || pending.ExecutionSnapshot.Capability == nil {
		if output == 0 {
			output = float64(input.Result.VideoDurationSeconds)
		}
		if reference == 0 {
			reference = float64(input.Result.VideoReferenceInputSeconds)
		}
	}
	salesOutput := output
	if pending.SalesSnapshot.QuantityPolicy == VideoQuantityRequestedSeconds {
		salesOutput = float64(pending.DurationSeconds)
	}
	salesQuantity, err := VideoBillingQuantity(pending.SalesSnapshot.BillingUnit, pending.SalesSnapshot.QuantityPolicy, pending.SalesSnapshot.MaxBillableSeconds, salesOutput, reference)
	if err != nil {
		return err
	}
	purchaseQuantity := float64(0)
	if pending.AccountCost != nil && pending.AccountCost.Version == VideoPricingVersion {
		purchaseOutput := output
		if pending.AccountCost.QuantityPolicy == VideoQuantityRequestedSeconds {
			purchaseOutput = float64(pending.DurationSeconds)
		}
		purchaseQuantity, err = VideoBillingQuantity(pending.AccountCost.BillingUnit, pending.AccountCost.QuantityPolicy, pending.AccountCost.MaxBillableSeconds, purchaseOutput, reference)
		if err != nil {
			return err
		}
	}
	var finalPurchaseCost *float64
	if input.AccountStatsCostOverride != nil {
		value := *input.AccountStatsCostOverride
		if input.AccountStatsRateMultiplierOverride != nil {
			value *= *input.AccountStatsRateMultiplierOverride
		}
		finalPurchaseCost = &value
	}
	payload, err := json.Marshal(videoTaskMeteringSnapshot{1, output, reference, pending.DurationSeconds,
		salesQuantity, purchaseQuantity, input.CostOverride.ActualCost, finalPurchaseCost})
	if err != nil {
		return err
	}
	return repo.SaveVideoTaskMeteringWithLease(ctx, pending.StateID, payload, lease)
}
