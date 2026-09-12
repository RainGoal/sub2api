package service

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

const seedanceSalesPricingKey = "seedance_sales_pricing"

func channelOwnsSeedanceSalesPricing(channel *Channel) bool {
	if channel == nil {
		return false
	}
	if enabled, _ := channel.FeaturesConfig[seedanceSalesPricingKey].(bool); enabled {
		return true
	}
	for _, pricing := range channel.ModelPricing {
		// Older channel forms allowed token/image/per-request entries for
		// Seedance, but video creation still used group prices. Do not interpret
		// those legacy rows as opting into the new per-second sales pricing.
		if pricing.Platform == PlatformSeedance && pricing.BillingMode == BillingModeVideo {
			return true
		}
	}
	return false
}

func preserveSeedanceSalesOwnership(channel *Channel) {
	if !channelOwnsSeedanceSalesPricing(channel) {
		return
	}
	if channel.FeaturesConfig == nil {
		channel.FeaturesConfig = make(map[string]any)
	}
	channel.FeaturesConfig[seedanceSalesPricingKey] = true
}

// Pricing ownership survives channel disablement. The normal channel lookup
// hides disabled channels, which would otherwise revive legacy group prices.
func (s *ChannelService) seedanceSalesChannelForGroup(ctx context.Context, groupID int64) (*Channel, error) {
	cache, err := s.loadCache(ctx)
	if err != nil {
		return nil, err
	}
	if cache.loadFailed {
		return nil, fmt.Errorf("seedance sales pricing channel cache is unavailable")
	}
	channel := cache.channelByGroupID[groupID]
	if channel == nil {
		return nil, nil
	}
	return channel.Clone(), nil
}

// ResolveSeedanceSalesPrice shares one lookup between validation, reservation
// and price display. Channel ownership prevents removed prices from reviving
// legacy group prices; groups without a migrated channel remain compatible.
func ResolveSeedanceSalesPrice(ctx context.Context, channels *ChannelService, group *Group, model, resolution string) (*float64, string, error) {
	if group == nil {
		return nil, "", fmt.Errorf("seedance sales pricing group is unavailable")
	}
	canonical, ok := videoprovider.CanonicalModel(model)
	if !ok {
		return nil, "", fmt.Errorf("unknown Seedance model")
	}
	tier, valid := LookupVideoBillingResolution(resolution)
	if !valid {
		return nil, "", fmt.Errorf("invalid Seedance sales price resolution")
	}
	var channel *Channel
	if channels != nil {
		var err error
		channel, err = channels.seedanceSalesChannelForGroup(ctx, group.ID)
		if err != nil {
			return nil, "", err
		}
	}
	if channelOwnsSeedanceSalesPricing(channel) {
		if !channel.IsActive() || !isVideoPriceResolutionSupported(canonical, tier) {
			return nil, PricingSourceChannel, nil
		}
		pricing := seedanceStatsPricingForModel(channel.ModelPricing, canonical)
		if pricing == nil {
			return nil, PricingSourceChannel, nil
		}
		if pricing.BillingMode != BillingModeVideo {
			return nil, PricingSourceChannel, fmt.Errorf("seedance sales prices require video billing")
		}
		price, err := seedanceStatsUnitPrice(pricing, tier)
		return price, PricingSourceChannel, err
	}
	return LookupVideoModelPrice(group.VideoModelPrices, model, resolution), PricingSourceGroup, nil
}

func normalizeSeedanceSalesPricing(prices []ChannelModelPricing) error {
	for i := range prices {
		if prices[i].Platform != PlatformSeedance {
			continue
		}
		if prices[i].BillingMode != BillingModeVideo {
			return infraerrors.BadRequest("INVALID_SEEDANCE_SALES_PRICE", "Seedance sales prices require video billing in USD/second")
		}
		if prices[i].TimePricing != nil && len(prices[i].TimePricing.Periods) > 0 {
			return infraerrors.BadRequest("INVALID_SEEDANCE_SALES_PRICE", "Seedance sales prices do not support time pricing")
		}
		if err := normalizeSeedanceStatsPricing(&prices[i]); err != nil {
			message := strings.ReplaceAll(infraerrors.Message(err), "account cost", "sales price")
			return infraerrors.BadRequest("INVALID_SEEDANCE_SALES_PRICE", message)
		}
	}
	return nil
}

// Legacy channel forms allowed other billing modes. Preserve unchanged rows
// during unrelated edits, without interpreting their units as USD/second.
func seedanceSalesPricingUnchanged(before, after []ChannelModelPricing) bool {
	configuration := func(prices []ChannelModelPricing) []ChannelModelPricing {
		var rows []ChannelModelPricing
		for _, price := range prices {
			if price.Platform != PlatformSeedance {
				continue
			}
			row := price.Clone()
			row.ID, row.ChannelID = 0, 0
			row.CreatedAt, row.UpdatedAt = time.Time{}, time.Time{}
			if len(row.Intervals) == 0 {
				row.Intervals = nil
			}
			for i := range row.Intervals {
				iv := &row.Intervals[i]
				iv.ID, iv.PricingID = 0, 0
				iv.CreatedAt, iv.UpdatedAt = time.Time{}, time.Time{}
			}
			rows = append(rows, row)
		}
		return rows
	}
	return reflect.DeepEqual(configuration(before), configuration(after))
}

// ProjectSeedanceSalesPrices returns a detached group for user-visible pricing.
// The legacy response field stays compatible while exposing the effective sales price.
func ProjectSeedanceSalesPrices(ctx context.Context, channels *ChannelService, group *Group) (*Group, error) {
	if group == nil || group.Platform != PlatformSeedance {
		return group, nil
	}
	projected := *group
	projected.VideoModelPrices = make(map[string]map[string]float64)
	for _, model := range videoprovider.DefaultModelIDs() {
		spec, ok := videoprovider.LookupModel(model)
		if !ok {
			continue
		}
		for _, resolution := range spec.Resolutions {
			price, _, err := ResolveSeedanceSalesPrice(ctx, channels, group, model, resolution)
			if err != nil {
				return nil, err
			}
			if price != nil {
				if projected.VideoModelPrices[spec.ID] == nil {
					projected.VideoModelPrices[spec.ID] = make(map[string]float64)
				}
				projected.VideoModelPrices[spec.ID][resolution] = *price
			}
		}
	}
	return &projected, nil
}

// PreviewSeedanceSalesImport is read-only. The normal channel save persists
// reviewed rows. Every selected Seedance group must have the same price map.
func (s *ChannelService) PreviewSeedanceSalesImport(ctx context.Context, groupIDs []int64) ([]ChannelModelPricing, error) {
	if s == nil || s.groupRepo == nil {
		return nil, fmt.Errorf("group repository is unavailable")
	}
	var baseline map[string]map[string]float64
	var baselineID int64
	seen := make(map[int64]bool)
	for _, groupID := range groupIDs {
		if seen[groupID] {
			continue
		}
		seen[groupID] = true
		group, err := s.groupRepo.GetByIDLite(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if group == nil || group.Platform != PlatformSeedance {
			continue
		}
		prices := NormalizeVideoModelPrices(group.VideoModelPrices)
		if baselineID != 0 && !reflect.DeepEqual(baseline, prices) {
			return nil, infraerrors.Conflict("SEEDANCE_LEGACY_PRICING_CONFLICT",
				fmt.Sprintf("Seedance groups %d and %d have different legacy prices; configure separate channels or review prices manually", baselineID, groupID))
		}
		baseline, baselineID = prices, groupID
	}
	models := make([]string, 0, len(baseline))
	for model := range baseline {
		if _, known := videoprovider.CanonicalModel(model); known {
			models = append(models, model)
		}
	}
	sort.Strings(models)
	prices := make([]ChannelModelPricing, 0, len(models))
	for _, model := range models {
		entry := ChannelModelPricing{Platform: PlatformSeedance, Models: []string{model}, BillingMode: BillingModeVideo}
		for _, resolution := range []string{"480p", "720p", "1080p", "4k"} {
			if price, ok := baseline[model][resolution]; ok {
				entry.Intervals = append(entry.Intervals, PricingInterval{TierLabel: resolution, PerRequestPrice: &price})
			}
		}
		prices = append(prices, entry)
	}
	return prices, nil
}
