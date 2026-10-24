package service

import (
	"context"
	"fmt"
	"strings"
)

type VideoBillingUnit string

const (
	VideoBillingPerSecond VideoBillingUnit = "per_second"
	VideoBillingPerVideo  VideoBillingUnit = "per_video"
	VideoPricingVersion                    = 1
)

type VideoQuantityPolicy string

const (
	VideoQuantityOutputSeconds             VideoQuantityPolicy = "output_seconds"
	VideoQuantityRequestedSeconds          VideoQuantityPolicy = "requested_seconds"
	VideoQuantityOutputAndReferenceSeconds VideoQuantityPolicy = "output_and_reference_seconds"
)

type VideoSalesPrice struct {
	BillingUnit        VideoBillingUnit    `json:"billing_unit"`
	UnitPrice          float64             `json:"unit_price"`
	Source             string              `json:"source"`
	Version            int                 `json:"version"`
	QuantityPolicy     VideoQuantityPolicy `json:"quantity_policy,omitempty"`
	MaxBillableSeconds float64             `json:"max_billable_seconds,omitempty"`
}

// VideoTaskSalesSnapshot freezes independently configured sales units and quantity
// policy. A nil task snapshot is reserved for the historical settlement formula.
type VideoTaskSalesSnapshot struct {
	Version            int                 `json:"version"`
	BillingUnit        VideoBillingUnit    `json:"billing_unit"`
	UnitPrice          float64             `json:"unit_price"`
	RateMultiplier     float64             `json:"rate_multiplier"`
	QuantityPolicy     VideoQuantityPolicy `json:"quantity_policy"`
	MaxBillableSeconds float64             `json:"max_billable_seconds,omitempty"`
}

func validateVideoQuantityConfig(unit VideoBillingUnit, policy VideoQuantityPolicy, maximum float64) error {
	if unit != VideoBillingPerSecond && unit != VideoBillingPerVideo {
		return fmt.Errorf("invalid video billing unit")
	}
	if (unit != VideoBillingPerVideo || policy != "") && policy != VideoQuantityOutputSeconds && policy != VideoQuantityOutputAndReferenceSeconds && policy != VideoQuantityRequestedSeconds {
		return fmt.Errorf("invalid video quantity policy")
	}
	if !validSeedanceAccountCost(maximum) {
		return fmt.Errorf("invalid video billing duration limit")
	}
	return nil
}

func VideoBillingQuantity(unit VideoBillingUnit, policy VideoQuantityPolicy, maximum, outputSeconds, referenceSeconds float64) (float64, error) {
	if err := validateVideoQuantityConfig(unit, policy, maximum); err != nil {
		return 0, err
	}
	if unit == VideoBillingPerVideo {
		return 1, nil
	}
	if !validSeedanceAccountCost(outputSeconds) || outputSeconds <= 0 || !validSeedanceAccountCost(referenceSeconds) {
		return 0, fmt.Errorf("video billing duration is missing or invalid")
	}
	quantity := outputSeconds
	if policy == VideoQuantityOutputAndReferenceSeconds {
		quantity += referenceSeconds
	}
	if !validSeedanceAccountCost(quantity) || (maximum > 0 && quantity > maximum) {
		return 0, fmt.Errorf("video billing duration exceeds its configured limit")
	}
	return quantity, nil
}

func (s *VideoTaskSalesSnapshot) Validate() error {
	if s == nil || s.Version != VideoPricingVersion {
		return fmt.Errorf("invalid video sales snapshot version")
	}
	if err := validateVideoQuantityConfig(s.BillingUnit, s.QuantityPolicy, s.MaxBillableSeconds); err != nil {
		return err
	}
	if !validSeedanceAccountCost(s.UnitPrice) || !validSeedanceAccountCost(s.RateMultiplier) || !validSeedanceAccountCost(s.UnitPrice*s.RateMultiplier) {
		return fmt.Errorf("invalid video sales price or multiplier")
	}
	return nil
}

func NewVideoTaskSalesSnapshot(price *VideoSalesPrice, multiplier float64, policy VideoQuantityPolicy, maxSeconds float64) (*VideoTaskSalesSnapshot, error) {
	if price == nil {
		return nil, fmt.Errorf("video sales price is missing")
	}
	if price.Version != 0 && price.Version != VideoPricingVersion {
		return nil, fmt.Errorf("invalid video sales price version")
	}
	if price.Version == VideoPricingVersion {
		policy, maxSeconds = price.QuantityPolicy, price.MaxBillableSeconds
	}
	snapshot := &VideoTaskSalesSnapshot{Version: VideoPricingVersion, BillingUnit: price.BillingUnit,
		UnitPrice: price.UnitPrice, RateMultiplier: multiplier, QuantityPolicy: policy, MaxBillableSeconds: maxSeconds}
	return snapshot, snapshot.Validate()
}

func (s *VideoTaskSalesSnapshot) Cost(outputSeconds, referenceSeconds float64) (*CostBreakdown, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	quantity, err := VideoBillingQuantity(s.BillingUnit, s.QuantityPolicy, s.MaxBillableSeconds, outputSeconds, referenceSeconds)
	if err != nil {
		return nil, err
	}
	total, actual := s.UnitPrice*quantity, s.UnitPrice*quantity*s.RateMultiplier
	if !validSeedanceAccountCost(total) || !validSeedanceAccountCost(actual) {
		return nil, fmt.Errorf("video sales amount exceeds the supported range")
	}
	return &CostBreakdown{TotalCost: total, ActualCost: actual, BillingMode: string(BillingModeVideo)}, nil
}

func (s *VideoTaskSalesSnapshot) HoldAmount(outputSeconds, referenceSeconds float64) (float64, error) {
	cost, err := s.Cost(outputSeconds, referenceSeconds)
	if err != nil {
		return 0, err
	}
	return QuantizeUsageBillingAmount(cost.ActualCost), nil
}

func normalizeExplicitVideoPricing(pricing *ChannelModelPricing) error {
	if pricing.VideoPricingVersion != VideoPricingVersion || pricing.Platform != PlatformSeedance || pricing.BillingMode != BillingModeVideo {
		return fmt.Errorf("explicit video pricing requires version 1 and video billing on the video platform")
	}
	if err := validateVideoQuantityConfig(pricing.VideoBillingUnit, pricing.VideoQuantityPolicy, pricing.VideoMaxBillableSeconds); err != nil {
		return err
	}
	if len(pricing.Models) == 0 {
		return fmt.Errorf("video pricing models are required")
	}
	for _, model := range pricing.Models {
		if strings.TrimSpace(model) == "" || strings.ContainsAny(model, "* \t\r\n") {
			return fmt.Errorf("explicit video pricing requires exact model IDs")
		}
	}
	if pricing.PerRequestPrice == nil && len(pricing.Intervals) == 0 {
		return fmt.Errorf("video pricing requires a default or resolution price")
	}
	if pricing.PerRequestPrice != nil && !validSeedanceAccountCost(*pricing.PerRequestPrice) {
		return fmt.Errorf("invalid video unit price")
	}
	seen := map[string]bool{}
	for i := range pricing.Intervals {
		iv := &pricing.Intervals[i]
		tier := strings.ToLower(strings.TrimSpace(iv.TierLabel))
		if tier == "" || seen[tier] || iv.PerRequestPrice == nil || !validSeedanceAccountCost(*iv.PerRequestPrice) {
			return fmt.Errorf("video resolution prices must be valid and unique")
		}
		iv.TierLabel, seen[tier] = tier, true
	}
	if pricing.TimePricing != nil && len(pricing.TimePricing.Periods) > 0 {
		return fmt.Errorf("video prices do not support time pricing")
	}
	return nil
}

// ResolveVideoSalesPrice exposes the actual billing unit. Legacy maps always
// remain per-second and cannot activate historical per_request sales rows.
func ResolveVideoSalesPrice(ctx context.Context, channels *ChannelService, group *Group, model, resolution string) (*VideoSalesPrice, error) {
	if group == nil || group.Platform != PlatformSeedance {
		return nil, fmt.Errorf("video sales group is unavailable")
	}
	if channels != nil {
		channel, err := channels.seedanceSalesChannelForGroup(ctx, group.ID)
		if err != nil {
			return nil, err
		}
		if channelOwnsSeedanceSalesPricing(channel) {
			if !channel.IsActive() {
				return nil, nil
			}
			pricing := seedanceStatsPricingForModel(channel.ModelPricing, model)
			if pricing != nil && pricing.VideoPricingVersion > 0 {
				if err := normalizeExplicitVideoPricing(pricing); err != nil {
					return nil, err
				}
				unitPrice, err := seedanceStatsUnitPrice(pricing, resolution)
				if err != nil || unitPrice == nil {
					return nil, err
				}
				return &VideoSalesPrice{Version: VideoPricingVersion, BillingUnit: pricing.VideoBillingUnit, UnitPrice: *unitPrice,
					Source: PricingSourceChannel, QuantityPolicy: pricing.VideoQuantityPolicy, MaxBillableSeconds: pricing.VideoMaxBillableSeconds}, nil
			}
		}
	}
	price, source, err := ResolveSeedanceSalesPrice(ctx, channels, group, model, resolution)
	if err != nil || price == nil {
		return nil, err
	}
	return &VideoSalesPrice{BillingUnit: VideoBillingPerSecond, UnitPrice: *price, Source: source}, nil
}

// ResolveVideoTaskAccountCost looks up the already resolved upstream model only
// on the selected account. It never applies model_mapping a second time.
// Generic protocols require this explicit price; legacy callers retain
// their historical estimate when this returns nil and requireExplicit is false.
func ResolveVideoTaskAccountCost(account *Account, model, resolution string, requireExplicit bool, policy VideoQuantityPolicy, maxSeconds float64) (*SeedanceAccountCostSnapshot, error) {
	pricing, err := LookupAccountModelCostPricing(account, model)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSeedanceAccountCostMissing, err)
	}
	if pricing == nil {
		if requireExplicit {
			return nil, ErrSeedanceAccountCostMissing
		}
		return nil, nil
	}
	price, err := seedanceStatsUnitPrice(pricing, resolution)
	if err != nil || price == nil {
		return nil, fmt.Errorf("%w for %s at %s", ErrSeedanceAccountCostMissing, model, resolution)
	}
	unit := VideoBillingPerSecond
	if pricing.BillingMode == BillingModePerRequest {
		unit = VideoBillingPerVideo
		policy = ""
	}
	if pricing.VideoPricingVersion != 0 {
		unit, policy, maxSeconds = pricing.VideoBillingUnit, pricing.VideoQuantityPolicy, pricing.VideoMaxBillableSeconds
	}
	snapshot := &SeedanceAccountCostSnapshot{Version: VideoPricingVersion, BillingMode: pricing.BillingMode,
		BillingUnit: unit, UnitPrice: *price, RateMultiplier: 1, QuantityPolicy: policy, MaxBillableSeconds: maxSeconds}
	return snapshot, snapshot.Validate()
}

// Cost uses exact provider seconds; older snapshots continue to consume the
// historical already-resolved billing duration in ApplySeedanceAccountCost.
func (s *SeedanceAccountCostSnapshot) Cost(outputSeconds, referenceSeconds float64) (float64, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	if s == nil || s.Version != VideoPricingVersion {
		return 0, fmt.Errorf("exact video purchase cost requires a versioned snapshot")
	}
	quantity, err := VideoBillingQuantity(s.BillingUnit, s.QuantityPolicy, s.MaxBillableSeconds, outputSeconds, referenceSeconds)
	if err != nil {
		return 0, err
	}
	cost := s.UnitPrice * quantity
	if !validSeedanceAccountCost(cost) || !validSeedanceAccountCost(cost*s.RateMultiplier) {
		return 0, fmt.Errorf("video purchase amount exceeds the supported range")
	}
	return cost, nil
}
