//go:build unit

package handler

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelPlazaVideoPricingContract(t *testing.T) {
	for _, unit := range []service.VideoBillingUnit{service.VideoBillingPerSecond, service.VideoBillingPerVideo} {
		t.Run(string(unit), func(t *testing.T) {
			pricing := &service.ChannelModelPricing{
				Platform: service.PlatformSeedance, BillingMode: service.BillingModeVideo,
				VideoPricingVersion: 1, VideoBillingUnit: unit,
				VideoQuantityPolicy: service.VideoQuantityOutputAndReferenceSeconds, VideoMaxBillableSeconds: 30.5,
				PerRequestPrice: testPtr(0.7),
				Intervals:       []service.PricingInterval{{TierLabel: "720p", PerRequestPrice: testPtr(0)}, {TierLabel: "1080p", PerRequestPrice: testPtr(0.5)}},
			}
			dto := toModelPlazaGroupDTO(&service.PlazaGroup{
				ID: 1, Platform: service.PlatformSeedance, VideoRateIndependent: true, VideoRateMultiplier: 0.5,
				Models: []service.PlazaModel{{Name: "seedance-2.0", Platform: service.PlatformSeedance, Pricing: pricing}},
			}, nil)
			raw, err := json.Marshal(dto.Models[0].Pricing)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(raw, &decoded))
			require.Equal(t, float64(1), decoded["video_pricing_version"])
			require.Equal(t, string(unit), decoded["video_billing_unit"])
			require.Equal(t, "output_and_reference_seconds", decoded["video_quantity_policy"])
			require.Equal(t, 30.5, decoded["video_max_billable_seconds"])
			require.Equal(t, 0.7, decoded["per_request_price"])
			intervals := decoded["intervals"].([]any)
			require.Equal(t, float64(0), intervals[0].(map[string]any)["per_request_price"])
			require.Equal(t, "1080p", intervals[1].(map[string]any)["tier_label"])
			require.NotContains(t, decoded, "id")
		})
	}
}

func TestUserPricingLegacyVideoUnitAndNonVideoOmission(t *testing.T) {
	legacy := &service.ChannelModelPricing{Platform: service.PlatformSeedance, BillingMode: service.BillingModeVideo}
	dto := toUserPricing(legacy)
	require.Equal(t, service.VideoBillingPerSecond, dto.VideoBillingUnit)
	require.Empty(t, dto.VideoQuantityPolicy)
	require.Empty(t, legacy.VideoBillingUnit, "conversion must not mutate shared pricing")
	for _, mode := range []service.BillingMode{service.BillingModeToken, service.BillingModeImage, service.BillingModePerRequest} {
		raw, err := json.Marshal(toUserPricing(&service.ChannelModelPricing{BillingMode: mode}))
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		for _, field := range []string{"video_pricing_version", "video_billing_unit", "video_quantity_policy", "video_max_billable_seconds"} {
			require.NotContains(t, decoded, field)
		}
	}
}
