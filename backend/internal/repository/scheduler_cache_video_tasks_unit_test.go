//go:build unit

package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCacheVideoProviderAdmission(t *testing.T) {
	for _, tc := range []struct {
		name         string
		provider     videoprovider.ID
		wantProvider videoprovider.ID
		selected     string
		allowed      string
		rejected     string
	}{
		{"JSON v1 built-in", videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderVideoTasksJSONV1, "minimax-h3", "minimax-h3", "grok-imagine-video"},
		{"legacy default", "", videoprovider.ProviderBBLabuV1, "Seedance-2.0", "seedance-2.0", "minimax-h3"},
		{"legacy explicit", videoprovider.ProviderBBLabuV1, videoprovider.ProviderBBLabuV1, "Seedance-2.5", "bytedance/seedance-2.5", "seedance-2.0-fast"},
		{"fflink fast", videoprovider.ProviderFFLinkV1, videoprovider.ProviderFFLinkV1, "seedance-2.0-fast", "seedance-2.0-fast", "Seedance-2.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := service.Account{
				ID: 341, Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key": "test-key", "base_url": "https://api.example.com/v1",
					"video_provider": string(tc.provider), "model_mapping": map[string]any{tc.selected: tc.selected},
				},
			}
			bucket := service.SchedulerBucket{Platform: service.PlatformSeedance, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
			full, err := cache.GetAccount(ctx, account.ID)
			require.NoError(t, err)
			require.NotNil(t, full)
			snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.True(t, hit)
			require.Len(t, snapshot, 1)
			for _, cached := range []*service.Account{full, snapshot[0]} {
				require.Equal(t, tc.wantProvider, cached.GetVideoProviderID())
				require.True(t, cached.IsModelSupported(tc.allowed), "selected model must survive caching")
				require.False(t, cached.IsModelSupported(tc.rejected), "caching must preserve model restrictions")
			}
		})
	}
}

func TestSchedulerCacheVideoCapabilitiesRefresh(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	custom, ok := videoprovider.LookupVideoModelCapability("minimax-h3", nil)
	require.True(t, ok)
	custom.Model = "Custom/Video-CaseSensitive"
	custom.DefaultResolution = "2K"
	disabled, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
	require.True(t, ok)
	disabled.Enabled = false
	account := service.Account{
		ID: 342, Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-key", "base_url": "https://api.example.com/v1",
			"video_provider": string(videoprovider.ProviderVideoTasksJSONV1),
			"model_mapping":  map[string]any{custom.Model: custom.Model, disabled.Model: disabled.Model},
		},
		Extra: map[string]any{service.VideoModelCapabilitiesExtra: []videoprovider.VideoModelCapability{custom, disabled}},
	}
	bucket := service.SchedulerBucket{Platform: service.PlatformSeedance, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))

	for _, stage := range []string{"enabled", "disabled", "cleared"} {
		t.Run(stage, func(t *testing.T) {
			switch stage {
			case "disabled":
				custom.Enabled = false
				account.Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{custom, disabled}
			case "cleared":
				account.Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{}
			}
			if stage != "enabled" {
				require.NoError(t, cache.SetAccount(ctx, &account))
			}
			snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.True(t, hit)
			require.Len(t, snapshot, 1)
			full, err := cache.GetAccount(ctx, account.ID)
			require.NoError(t, err)
			require.NotNil(t, full)
			want, err := account.VideoModelCapabilities()
			require.NoError(t, err)
			for _, cached := range []*service.Account{snapshot[0], full} {
				require.Contains(t, cached.Extra, service.VideoModelCapabilitiesExtra)
				got, err := cached.VideoModelCapabilities()
				require.NoError(t, err)
				require.Equal(t, want, got, "capabilities must preserve the account contract")
				require.Equal(t, stage == "enabled", cached.IsModelSupported(custom.Model))
				require.False(t, cached.IsModelSupported(strings.ToLower(custom.Model)), "allowlist names are exact")
				require.Equal(t, stage == "cleared", cached.IsModelSupported(disabled.Model), "disabled overrides must be removed on clear")
			}
		})
	}
}
