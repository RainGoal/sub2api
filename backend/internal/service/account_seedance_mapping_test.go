package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestSeedanceUpstreamModelPreservesLegacyDefaultsAndExplicitTargets(t *testing.T) {
	for _, tc := range []struct {
		name, provider, public, target, expected string
	}{
		{"bblabu default", "bblabu_v1", "seedance-2.0", "", "Seedance-2.0"},
		{"bblabu legacy selection", "bblabu_v1", "seedance-2.0", "seedance-2.0", "Seedance-2.0"},
		{"bblabu public alias", "bblabu_v1", "studio-video", "Seedance-2.0", "Seedance-2.0"},
		{"bblabu explicit spelling", "bblabu_v1", "studio-video", "seedance-2.0", "seedance-2.0"},
		{"bblabu case mapping", "bblabu_v1", "seedance-2.0", "Seedance-2.0", "Seedance-2.0"},
		{"fflink default", "fflink_v1", "Seedance-2.0", "", "seedance-2.0"},
		{"fflink prefixed alias", "fflink_v1", "bytedance/seedance-2.5", "", "bytedance/seedance-2.5"},
		{"fflink explicit spelling", "fflink_v1", "studio-video", "Seedance-2.0", "Seedance-2.0"},
		{"cross model", "fflink_v1", "seedance-2.0", "seedance-2.5", "seedance-2.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Platform: PlatformSeedance, Credentials: map[string]any{"video_provider": tc.provider}}
			if tc.target != "" {
				account.Credentials["model_mapping"] = map[string]any{tc.public: tc.target}
			}
			actual, err := account.ResolveSeedanceUpstreamModel(tc.public)
			require.NoError(t, err)
			require.Equal(t, tc.expected, actual)
			require.True(t, account.IsModelSupported(tc.public))
			require.Equal(t, tc.target != "" && tc.target != tc.public, account.hasSeedanceModelRemapping(tc.public))
		})
	}
}

func TestSeedanceExplicitMappingDoesNotAuthorizeTargetOrMapTwice(t *testing.T) {
	account := &Account{Platform: PlatformSeedance, Credentials: map[string]any{
		"video_provider": "fflink_v1", "model_mapping": map[string]any{
			"studio-video": "seedance-2.0", "seedance-2.0": "seedance-2.5",
		},
	}}
	upstream, err := account.ResolveSeedanceUpstreamModel("studio-video")
	require.NoError(t, err)
	require.Equal(t, "seedance-2.0", upstream)
	require.False(t, account.IsModelSupported("seedance-2.5"))
	require.False(t, account.IsModelSupported("Studio-video"))
	require.False(t, account.IsModelSupported("Seedance-2.0"), "explicit remapping keys are exact")
	account.Credentials["model_mapping"] = map[string]any{"studio-video": "kling-3.0"}
	_, err = account.ResolveSeedanceUpstreamModel("studio-video")
	require.Error(t, err)
}

func TestCapabilitySeedanceMappingKeepsSupplierSpelling(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1} {
		capability := videoProtocolCapability(provider)
		account := videoProtocolAccount(1, provider, capability)
		account.Credentials["model_mapping"] = map[string]any{capability.Model: "Supplier/Exact-Model"}
		upstream, err := account.ResolveSeedanceUpstreamModel(capability.Model)
		require.NoError(t, err)
		require.Equal(t, "Supplier/Exact-Model", upstream)
		_, err = account.ResolveSeedanceUpstreamModel("Supplier/Exact-Model")
		require.Error(t, err)
	}
}

func TestSeedanceMappedPresetUsesUpstreamBeforePublicAlias(t *testing.T) {
	account := &Account{Platform: PlatformSeedance, Credentials: map[string]any{
		"video_provider": "video_tasks_json_v1", "model_mapping": map[string]any{"seedance-2.0": "Seedance-2.5"},
	}}
	capability, ok := account.VideoModelCapability("seedance-2.0")
	require.True(t, ok)
	require.Equal(t, 30, capability.MaxDuration, "the target preset supplies the actual limits")
	require.Equal(t, "seedance-2.0", capability.Model)
	require.Equal(t, "Seedance-2.5", capability.UpstreamModel)
	upstream := capability
	upstream.Model, upstream.UpstreamModel, upstream.MaxDuration = "Seedance-2.5", "", 20
	account.Extra = map[string]any{VideoModelCapabilitiesExtra: []videoprovider.VideoModelCapability{upstream}}
	capability, ok = account.VideoModelCapability("seedance-2.0")
	require.True(t, ok)
	require.Equal(t, 20, capability.MaxDuration, "account-local upstream overrides still beat presets")
	public := upstream
	public.Model, public.MaxDuration = "seedance-2.0", 10
	account.Extra[VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{upstream, public}
	capability, ok = account.VideoModelCapability("seedance-2.0")
	require.True(t, ok)
	require.Equal(t, 10, capability.MaxDuration, "the explicit public contract remains highest priority")
	public.Enabled = false
	account.Extra[VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{upstream, public}
	require.False(t, account.IsModelSupported("seedance-2.0"))
}
