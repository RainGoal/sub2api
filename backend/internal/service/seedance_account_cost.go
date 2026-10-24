package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

var ErrSeedanceAccountCostMissing = errors.New("seedance account cost is not configured")

// SeedanceAccountCostSnapshot freezes the upstream statistics price before the
// provider accepts a task. UnitPrice is USD/second for video, USD/task otherwise.
// Account statistics apply RateMultiplier once; user prices and quotas are separate.
type SeedanceAccountCostSnapshot struct {
	BillingMode        BillingMode         `json:"billing_mode"`
	UnitPrice          float64             `json:"unit_price"`
	RateMultiplier     float64             `json:"rate_multiplier"`
	Version            int                 `json:"version,omitempty"`
	BillingUnit        VideoBillingUnit    `json:"billing_unit,omitempty"`
	QuantityPolicy     VideoQuantityPolicy `json:"quantity_policy,omitempty"`
	MaxBillableSeconds float64             `json:"max_billable_seconds,omitempty"`
}

func validSeedanceAccountCost(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (s *SeedanceAccountCostSnapshot) Validate() error {
	if s == nil {
		return nil // Tasks created before cost snapshots remain readable.
	}
	if s.BillingMode != BillingModeVideo && s.BillingMode != BillingModePerRequest {
		return fmt.Errorf("invalid Seedance account cost billing mode")
	}
	if s.Version != 0 {
		if s.Version != VideoPricingVersion {
			return fmt.Errorf("invalid video purchase snapshot version")
		}
		if err := validateVideoQuantityConfig(s.BillingUnit, s.QuantityPolicy, s.MaxBillableSeconds); err != nil {
			return err
		}
		if s.BillingMode == BillingModePerRequest && s.BillingUnit != VideoBillingPerVideo {
			return fmt.Errorf("legacy per_request video purchase prices must remain per_video")
		}
	} else if s.BillingUnit != "" || s.QuantityPolicy != "" || s.MaxBillableSeconds != 0 {
		return fmt.Errorf("explicit video purchase units require a versioned snapshot")
	}
	for _, value := range []float64{s.UnitPrice, s.RateMultiplier, s.UnitPrice * s.RateMultiplier} {
		if !validSeedanceAccountCost(value) {
			return fmt.Errorf("invalid Seedance account cost price or multiplier")
		}
	}
	return nil
}

func seedanceStatsPricingForModel(prices []ChannelModelPricing, model string) *ChannelModelPricing {
	for i := range prices {
		if prices[i].Platform != PlatformSeedance || prices[i].VideoPricingVersion == 0 {
			continue
		}
		for _, candidate := range prices[i].Models {
			if strings.TrimSpace(candidate) == strings.TrimSpace(model) {
				return &prices[i]
			}
		}
	}
	canonical, known := videoprovider.CanonicalModel(model)
	if !known {
		return nil
	}
	// Canonical aliases have the same exact-match priority, ahead of wildcards.
	for i := range prices {
		if prices[i].VideoPricingVersion != 0 || !isPlatformMatch(PlatformSeedance, prices[i].Platform) {
			continue
		}
		for _, candidate := range prices[i].Models {
			if normalized, ok := videoprovider.CanonicalModel(candidate); ok && normalized == canonical {
				return &prices[i]
			}
		}
	}
	return findPricingForModel(legacyVideoPricingRows(prices), PlatformSeedance, canonical)
}

func legacyVideoPricingRows(prices []ChannelModelPricing) []ChannelModelPricing {
	legacy := make([]ChannelModelPricing, 0, len(prices))
	for _, price := range prices {
		if price.VideoPricingVersion == 0 {
			legacy = append(legacy, price)
		}
	}
	return legacy
}

func seedanceStatsUnitPrice(pricing *ChannelModelPricing, resolution string) (*float64, error) {
	if pricing.VideoPricingVersion != 0 {
		if err := normalizeExplicitVideoPricing(pricing); err != nil {
			return nil, err
		}
		for _, interval := range pricing.Intervals {
			if strings.EqualFold(interval.TierLabel, strings.TrimSpace(resolution)) {
				return interval.PerRequestPrice, nil
			}
		}
		return pricing.PerRequestPrice, nil
	}
	if pricing.BillingMode == BillingModePerRequest {
		return pricing.PerRequestPrice, nil
	}
	if pricing.BillingMode != BillingModeVideo {
		return nil, fmt.Errorf("seedance account costs require video or per_request pricing")
	}
	tier, valid := LookupVideoBillingResolution(resolution)
	if !valid {
		return nil, fmt.Errorf("invalid Seedance account cost resolution")
	}
	for _, interval := range pricing.Intervals {
		if normalized, ok := LookupVideoBillingResolution(strings.TrimSpace(interval.TierLabel)); ok && normalized == tier {
			return interval.PerRequestPrice, nil
		}
	}
	return pricing.PerRequestPrice, nil
}

func normalizeSeedanceStatsPricing(pricing *ChannelModelPricing) error {
	if pricing.Platform != PlatformSeedance {
		return nil
	}
	if pricing.VideoPricingVersion != 0 {
		return normalizeExplicitVideoPricing(pricing)
	}
	if pricing.VideoBillingUnit != "" || pricing.VideoQuantityPolicy != "" || pricing.VideoMaxBillableSeconds != 0 {
		return fmt.Errorf("explicit video units require video_pricing_version 1")
	}
	invalid := func(message string) error {
		return infraerrors.BadRequest("INVALID_SEEDANCE_ACCOUNT_COST", message)
	}
	if len(pricing.Models) == 0 {
		return invalid("Seedance account cost models are required")
	}
	for i, model := range pricing.Models {
		if canonical, ok := videoprovider.CanonicalModel(model); ok {
			pricing.Models[i] = canonical
		}
	}
	if pricing.BillingMode != BillingModeVideo && pricing.BillingMode != BillingModePerRequest {
		return invalid("Seedance account costs require video or per_request pricing")
	}
	if pricing.BillingMode == BillingModePerRequest && (pricing.PerRequestPrice == nil || len(pricing.Intervals) > 0) {
		return invalid("Seedance per_request cost requires a fixed price without tiers")
	}
	if pricing.PerRequestPrice != nil && !validSeedanceAccountCost(*pricing.PerRequestPrice) {
		return invalid("Seedance account cost must be finite and nonnegative")
	}
	seen := map[string]bool{}
	for i := range pricing.Intervals {
		interval := &pricing.Intervals[i]
		tier, ok := LookupVideoBillingResolution(interval.TierLabel)
		if !ok || seen[tier] {
			return invalid("Seedance account cost resolutions must be valid and unique")
		}
		for _, model := range pricing.Models {
			if canonical, known := videoprovider.CanonicalModel(model); known && !isVideoPriceResolutionSupported(canonical, tier) {
				return invalid(fmt.Sprintf("Seedance model %s does not support %s", model, tier))
			}
		}
		if interval.PerRequestPrice == nil || !validSeedanceAccountCost(*interval.PerRequestPrice) {
			return invalid("each Seedance account cost resolution requires a finite nonnegative price")
		}
		interval.TierLabel = tier
		seen[tier] = true
	}
	return nil
}

func (s *OpenAIGatewayService) resolveSeedanceAccountCost(ctx context.Context, pending *SeedanceVideoPendingBilling, account *Account) (*SeedanceAccountCostSnapshot, error) {
	snapshot := &SeedanceAccountCostSnapshot{
		BillingMode: BillingModeVideo, UnitPrice: pending.TotalCostPerSecond,
		RateMultiplier: account.BillingRateMultiplier(),
	}
	upstreamModel, err := account.ResolveSeedanceUpstreamModel(pending.Model)
	if err != nil {
		return nil, err
	}
	pricing, err := LookupAccountModelCostPricing(account, upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSeedanceAccountCostMissing, err)
	}
	if pricing != nil {
		price, err := seedanceStatsUnitPrice(pricing, pending.Resolution)
		if err != nil || price == nil {
			return nil, fmt.Errorf("%w for %s at %s", ErrSeedanceAccountCostMissing, upstreamModel, pending.Resolution)
		}
		snapshot.BillingMode = pricing.BillingMode
		snapshot.UnitPrice = *price
		snapshot.RateMultiplier = 1 // Explicit prices are the actual purchase price.
		if pricing.VideoPricingVersion != 0 {
			snapshot.Version, snapshot.BillingUnit = VideoPricingVersion, pricing.VideoBillingUnit
			snapshot.QuantityPolicy, snapshot.MaxBillableSeconds = pricing.VideoQuantityPolicy, pricing.VideoMaxBillableSeconds
		}
	} else if account.hasSeedanceModelRemapping(pending.Model) {
		return nil, fmt.Errorf("%w for %s at %s", ErrSeedanceAccountCostMissing, upstreamModel, pending.Resolution)
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// ApplySeedanceAccountCost attaches statistics-only overrides for both HTTP and
// recovery settlement. A nil snapshot preserves the behavior of historical tasks.
func ApplySeedanceAccountCost(input *OpenAIRecordUsageInput, pending *SeedanceVideoPendingBilling) error {
	if pending == nil || pending.AccountCost == nil {
		return nil
	}
	snapshot := pending.AccountCost
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if input == nil || input.Result == nil {
		return fmt.Errorf("seedance account cost settlement input is missing")
	}
	cost := snapshot.UnitPrice
	if snapshot.Version == VideoPricingVersion {
		outputSeconds := input.Result.VideoDurationSecondsExact
		if outputSeconds == 0 {
			outputSeconds = float64(input.Result.VideoDurationSeconds)
		}
		referenceSeconds := input.Result.VideoReferenceInputSecondsExact
		if referenceSeconds == 0 {
			referenceSeconds = float64(input.Result.VideoReferenceInputSeconds)
		}
		if snapshot.QuantityPolicy == VideoQuantityRequestedSeconds {
			outputSeconds = float64(pending.DurationSeconds)
		}
		quantity, err := VideoBillingQuantity(snapshot.BillingUnit, snapshot.QuantityPolicy, snapshot.MaxBillableSeconds,
			outputSeconds, referenceSeconds)
		if err != nil {
			return err
		}
		cost *= quantity
	} else if snapshot.BillingMode == BillingModeVideo {
		if input.Result.VideoBillingDurationSeconds <= 0 {
			return fmt.Errorf("seedance account cost billing duration is missing")
		}
		cost *= float64(input.Result.VideoBillingDurationSeconds)
	}
	if math.IsNaN(cost) || math.IsInf(cost, 0) || math.IsInf(cost*snapshot.RateMultiplier, 0) {
		return fmt.Errorf("seedance account cost exceeds the supported range")
	}
	multiplier := snapshot.RateMultiplier
	input.AccountStatsCostOverride = &cost
	input.AccountStatsRateMultiplierOverride = &multiplier
	return nil
}

func (s *OpenAIGatewayService) RecordSeedanceVideoUsage(ctx context.Context, pending *SeedanceVideoPendingBilling, input *OpenAIRecordUsageInput) error {
	if pending != nil && pending.IDVersion == SeedanceVideoTaskIdentityVersion && input != nil {
		input.VideoTaskStateID, input.VideoTaskLeaseUntil = pending.StateID, pending.LeaseUntil
		input.BalanceHoldID, input.BalanceHoldAmount = pending.HoldID, pending.HoldAmount
	}
	reusedMetering := false
	if input != nil {
		result, cost, err := s.frozenVideoTaskCompletion(ctx, pending, input.Result)
		if err != nil {
			return err
		}
		if result != nil {
			if input.CostOverride == nil {
				return fmt.Errorf("video metering settlement cost is unavailable")
			}
			*input.Result, *input.CostOverride = *result, *cost
			reusedMetering = true
		}
	}
	if err := ApplySeedanceAccountCost(input, pending); err != nil {
		return err
	}
	if !reusedMetering {
		if err := s.PersistVideoTaskMetering(ctx, pending, input); err != nil {
			return err
		}
	}
	return s.RecordUsage(ctx, input)
}
