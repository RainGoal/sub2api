package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeedanceVideoPricesMergeAliasesAndKeepModelVariants(t *testing.T) {
	prices := map[string]map[string]float64{
		"seedance-2.0":           {"720p": 0.1, "2160p": 0.4},
		"seedance-2.0-fast":      {"720p": 0.05, "1080p": 0.9},
		"seedance-2.0-mini":      {"1080p": 0.08, "4k": 0.9},
		"bytedance/seedance-2.5": {"480p": 0.2, "720p": 0.9},
		"seedance-2.5":           {"720p": 0.3},
	}
	normalized := NormalizeVideoModelPrices(prices)
	require.Equal(t, map[string]map[string]float64{
		"seedance-2.0":      {"720p": 0.1, "4k": 0.4},
		"seedance-2.0-fast": {"720p": 0.05},
		"seedance-2.0-mini": {"1080p": 0.08},
		"seedance-2.5":      {"480p": 0.2, "720p": 0.3},
	}, normalized)
	for _, model := range []string{"seedance-2.5", "bytedance/seedance-2.5", "Seedance-2.5"} {
		price := LookupVideoModelPrice(normalized, model, "720p")
		require.NotNil(t, price, model)
		require.Equal(t, 0.3, *price, model)
		legacyPrice := LookupVideoModelPrice(prices, model, "480p")
		require.NotNil(t, legacyPrice, model)
		require.Equal(t, 0.2, *legacyPrice, model)
	}
	price := LookupVideoModelPrice(normalized, "seedance-2.0", "2160p")
	require.NotNil(t, price)
	require.Equal(t, 0.4, *price)
}

func TestSeedanceVideoPriceLookupRequiresSupportedResolution(t *testing.T) {
	for _, model := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		prices := map[string]map[string]float64{model: {"480p": 0.1}}
		for _, resolution := range []string{"", "auto", "400p", "544p", "960p", "1440p", "1080i"} {
			require.Nil(t, LookupVideoModelPrice(prices, model, resolution), "%s / %s", model, resolution)
		}
	}
	for _, model := range []string{"seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		prices := map[string]map[string]float64{model: {"480p": 0.1, "4k": 0.9}}
		require.Nil(t, LookupVideoModelPrice(prices, model, "4k"), model)
	}
	prices := map[string]map[string]float64{"seedance-2.5": {"480p": 0.1, "1080p": 0.9}}
	require.Nil(t, LookupVideoModelPrice(prices, "seedance-2.5", "1080p"))
}

func TestGrokVideoPriceLookupKeepsLegacyResolutionFallback(t *testing.T) {
	prices := map[string]map[string]float64{VideoPriceFamilyGrokImagineVideo: {"480p": 0.05}}
	for _, resolution := range []string{"", "auto", "unknown"} {
		price := LookupVideoModelPrice(prices, VideoPriceFamilyGrokImagineVideo, resolution)
		require.NotNil(t, price)
		require.Equal(t, 0.05, *price)
	}
}
