package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

// AccountModelCostPricingExtraKey stores actual purchase prices on the selected account.
// The prices are independent of customer groups and the account quota multiplier.
const AccountModelCostPricingExtraKey = "model_cost_pricing"

func invalidAccountModelCost(message string) error {
	return infraerrors.BadRequest("INVALID_ACCOUNT_MODEL_COST_PRICING", message)
}

// NormalizeAccountModelCostPricingExtra validates and normalizes an explicitly supplied
// price list. An omitted key is unchanged; [] explicitly clears the configuration.
func NormalizeAccountModelCostPricingExtra(platform string, extra map[string]any) error {
	raw, provided := extra[AccountModelCostPricingExtraKey]
	if !provided {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) == 0 || data[0] != '[' {
		return invalidAccountModelCost("model_cost_pricing must be an array")
	}
	var prices []ChannelModelPricing
	if err := json.Unmarshal(data, &prices); err != nil {
		return invalidAccountModelCost("invalid model cost price list")
	}
	for i := range prices {
		p := &prices[i]
		if err := normalizeAccountModelCostEntry(platform, p); err != nil {
			return invalidAccountModelCost(fmt.Sprintf("entry #%d: %v", i+1, err))
		}
	}
	if err := validatePricingEntries(prices); err != nil {
		return err
	}
	extra[AccountModelCostPricingExtraKey] = prices
	return nil
}

func normalizeAccountCostModel(platform, model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if platform == PlatformSeedance {
		if canonical, ok := videoprovider.CanonicalModel(model); ok {
			return canonical
		}
	}
	return model
}

func normalizeAccountModelCostEntry(platform string, p *ChannelModelPricing) error {
	if p.Platform != "" && p.Platform != platform {
		return fmt.Errorf("price platform must match the account platform")
	}
	p.Platform = platform
	p.ID, p.ChannelID = 0, 0
	if len(p.Models) == 0 {
		return fmt.Errorf("models are required")
	}
	for i, model := range p.Models {
		model = normalizeAccountCostModel(platform, model)
		if model == "" || strings.ContainsAny(model, " \t\r\n") ||
			(strings.Contains(model, "*") && (!strings.HasSuffix(model, "*") || strings.Count(model, "*") != 1)) {
			return fmt.Errorf("invalid model pattern")
		}
		p.Models[i] = model
	}
	if p.BillingMode == "" {
		p.BillingMode = BillingModeToken
	}
	switch p.BillingMode {
	case BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo:
	default:
		return fmt.Errorf("unsupported billing mode")
	}
	if platform == PlatformSeedance && p.BillingMode != BillingModeVideo && p.BillingMode != BillingModePerRequest {
		return fmt.Errorf("seedance costs require video or per_request billing")
	}
	if p.TimePricing != nil && len(p.TimePricing.Periods) > 0 {
		return fmt.Errorf("account purchase prices do not support time pricing")
	}
	p.TimePricing = nil
	// Ordinary gateways settle after the provider succeeds. Require a default
	// so an uncovered context/size cannot turn into a late billing failure.
	// Seedance checks the selected resolution before submitting an async task.
	if p.BillingMode == BillingModeToken && !hasAccountTokenCostPrice(p) {
		return fmt.Errorf("token cost requires at least one default price; intervals may override it")
	}
	if platform != PlatformSeedance && p.BillingMode != BillingModeToken && p.PerRequestPrice == nil {
		return fmt.Errorf("media and per-request costs require a default unit price; tiers may override it")
	}
	if p.MaxReasoningEffortMultiplier != nil && *p.MaxReasoningEffortMultiplier <= 0 {
		return fmt.Errorf("max reasoning multiplier must be positive")
	}
	seenTiers := make(map[string]bool)
	for i := range p.Intervals {
		iv := &p.Intervals[i]
		iv.ID, iv.PricingID = 0, 0
		iv.TierLabel = strings.TrimSpace(iv.TierLabel)
		if p.BillingMode == BillingModeToken {
			if iv.TierLabel != "" || iv.PerRequestPrice != nil {
				return fmt.Errorf("token intervals require token prices and ranges")
			}
			continue
		}
		if iv.PerRequestPrice == nil {
			return fmt.Errorf("each media tier requires an explicit unit price")
		}
		tier := strings.ToLower(iv.TierLabel)
		if p.BillingMode == BillingModeVideo {
			canonical, ok := LookupVideoBillingResolution(tier)
			if !ok {
				return fmt.Errorf("invalid video resolution")
			}
			tier, iv.TierLabel = canonical, canonical
			for _, model := range p.Models {
				if canonicalModel, ok := videoprovider.CanonicalModel(model); ok && !isVideoPriceResolutionSupported(canonicalModel, tier) {
					return fmt.Errorf("model %s does not support %s", model, tier)
				}
			}
		}
		if tier == "" || seenTiers[tier] {
			return fmt.Errorf("media tiers must have unique nonempty labels")
		}
		seenTiers[tier] = true
	}
	if platform == PlatformSeedance && p.BillingMode == BillingModePerRequest && len(p.Intervals) > 0 {
		return fmt.Errorf("seedance per_request cost is a fixed price per task")
	}
	return nil
}

func hasAccountTokenCostPrice(p *ChannelModelPricing) bool {
	return p.InputPrice != nil || p.OutputPrice != nil || p.CacheWritePrice != nil ||
		p.CacheWrite1hPrice != nil || p.CacheReadPrice != nil || p.ImageInputPrice != nil || p.ImageOutputPrice != nil
}

// LookupAccountModelCostPricing matches the selected account's upstream model,
// without inheriting a parent account, customer group, or channel price.
func LookupAccountModelCostPricing(account *Account, model string) (*ChannelModelPricing, error) {
	if account == nil || strings.TrimSpace(model) == "" {
		return nil, nil
	}
	raw, exists := account.Extra[AccountModelCostPricingExtraKey]
	if !exists {
		return nil, nil
	}
	// Work on a private copy: account snapshots may be shared across requests.
	extra := map[string]any{AccountModelCostPricingExtraKey: raw}
	if err := NormalizeAccountModelCostPricingExtra(account.Platform, extra); err != nil {
		return nil, err
	}
	prices, ok := extra[AccountModelCostPricingExtraKey].([]ChannelModelPricing)
	if !ok {
		return nil, invalidAccountModelCost("invalid normalized model cost price list")
	}
	return findPricingForModel(prices, account.Platform, normalizeAccountCostModel(account.Platform, model)), nil
}

// ResolveAccountModelCost returns an actual purchase cost (zero is a valid price).
// nil means this model has no account price. Video UsageUnits are provider billing
// seconds; per_request RequestCount is successful tasks, image RequestCount is images.
func ResolveAccountModelCost(ctx context.Context, billing *BillingService, account *Account, input CostInput) (*float64, error) {
	pricing, err := LookupAccountModelCostPricing(account, input.Model)
	if err != nil || pricing == nil {
		return nil, err
	}
	var cost float64
	if pricing.BillingMode == BillingModeToken {
		if billing == nil {
			return nil, fmt.Errorf("account model cost billing service is unavailable")
		}
		resolver := NewModelPricingResolver(nil, nil)
		resolved := &ResolvedPricing{BasePricing: &ModelPricing{CacheCreationPriceExplicit: true}, channelPricing: pricing}
		resolver.applyTokenOverrides(pricing, resolved)
		contextTokens := input.Tokens.InputTokens + input.Tokens.CacheCreationTokens + input.Tokens.CacheReadTokens
		base := resolver.GetIntervalPricing(resolved, contextTokens)
		one := 1.0
		if base.FastMultiplier == nil {
			base.FastMultiplier = &one
		}
		if base.FlexMultiplier == nil {
			base.FlexMultiplier = &one
		}
		// No official model policy or default service-tier discount may leak into
		// a negotiated purchase price. Reuse only the shared token arithmetic.
		tokens := input.Tokens
		if pricing.ImageInputPrice != nil && *pricing.ImageInputPrice == 0 && tokens.ImageInputTokens > 0 {
			tokens.InputTokens = max(0, tokens.InputTokens-tokens.ImageInputTokens)
			tokens.ImageInputTokens = 0
		}
		cost = billing.computeTokenBreakdown(base, tokens, 1, input.ServiceTier, false).TotalCost
		if pricing.MaxReasoningEffortMultiplier != nil && NormalizeMaxReasoningEffort(input.ReasoningEffort) == "max" {
			cost *= *pricing.MaxReasoningEffortMultiplier
		}
	} else {
		unitPrice := pricing.PerRequestPrice
		for _, tier := range pricing.Intervals {
			if strings.EqualFold(tier.TierLabel, strings.TrimSpace(input.SizeTier)) {
				unitPrice = tier.PerRequestPrice
				break
			}
		}
		if unitPrice == nil {
			return nil, invalidAccountModelCost("matched model has no purchase price for the requested tier")
		}
		units := float64(max(1, input.RequestCount))
		if pricing.BillingMode == BillingModeVideo {
			units = input.UsageUnits
			if units <= 0 {
				return nil, invalidAccountModelCost("video purchase cost requires positive billing seconds")
			}
		}
		cost = *unitPrice * units
	}
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return nil, invalidAccountModelCost("account purchase cost exceeds the supported range")
	}
	return &cost, nil
}

// Image batches can contain several billing resolutions; per-request purchase
// prices still apply once to the successful request, not once per image.
func resolveAccountModelCostWithImages(ctx context.Context, billing *BillingService, account *Account,
	input CostInput, imageCount int, sizes map[string]int,
) (*float64, error) {
	pricing, err := LookupAccountModelCostPricing(account, input.Model)
	if err != nil || pricing == nil {
		return nil, err
	}
	if pricing.BillingMode != BillingModeImage {
		return ResolveAccountModelCost(ctx, billing, account, input)
	}
	input.RequestCount = imageCount
	if len(sizes) == 0 {
		return ResolveAccountModelCost(ctx, billing, account, input)
	}
	total, counted := 0.0, 0
	for size, count := range sizes {
		if count <= 0 {
			continue
		}
		tierInput := input
		tierInput.SizeTier, tierInput.RequestCount = size, count
		cost, err := ResolveAccountModelCost(ctx, billing, account, tierInput)
		if err != nil {
			return nil, err
		}
		total += *cost
		counted += count
	}
	// Some providers return dimensions for only part of the images. Use the
	// request's resolved billing tier for the remaining images, as sales do.
	if counted < imageCount {
		input.RequestCount = imageCount - counted
		cost, err := ResolveAccountModelCost(ctx, billing, account, input)
		if err != nil {
			return nil, err
		}
		total += *cost
	}
	if counted > imageCount || math.IsInf(total, 0) {
		return nil, invalidAccountModelCost("image cost size breakdown does not match the image count")
	}
	return &total, nil
}
