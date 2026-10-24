package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestZhiqiAccountPublicMappingAndDefaultBaseURL(t *testing.T) {
	for _, storedModel := range []string{"studio-video", "Provider/VideoRoute"} {
		t.Run(storedModel, func(t *testing.T) {
			c := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
			c.Model = storedModel
			account := videoTasksGatewayAccount(71, c)
			account.Credentials = map[string]any{"api_key": "test-key", "video_provider": "zhiqi",
				"model_mapping": map[string]any{"studio-video": "Provider/VideoRoute"}}
			require.NoError(t, validateSeedanceAccountCredentials(account.Platform, account.Type, account.Credentials))
			require.NoError(t, ValidateVideoTasksAccount(account))
			require.Equal(t, videoprovider.ProviderZhiqiV1, account.GetVideoProviderID())
			require.Equal(t, videoprovider.DefaultZhiqiBaseURL, account.GetSeedanceBaseURL())
			require.True(t, account.IsModelSupported("studio-video"))
			require.False(t, account.IsModelSupported("Provider/VideoRoute"))
			resolved, ok := account.VideoModelCapability("studio-video")
			require.True(t, ok)
			require.Equal(t, "studio-video", resolved.Model)
			require.Equal(t, "Provider/VideoRoute", resolved.UpstreamModel)
			require.Equal(t, storedModel, c.Model, "mapping must not mutate the stored capability")
		})
	}
}

func TestZhiqiAccountRejectsIncompatibleProfilesAndMappingConflicts(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderZhiqiV1, videoprovider.ProviderVideoTasksJSONV1} {
		c := videoProtocolCapability(provider)
		account := videoProtocolAccount(72, provider, c)
		if provider == videoprovider.ProviderZhiqiV1 {
			c.Profile = videoprovider.ProfileBBLabuRoute
		} else {
			c.Profile = videoprovider.ProfileZhiqiV1
		}
		account.Extra[VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{c}
		require.Error(t, ValidateVideoTasksAccount(account))
		_, ok := account.VideoModelCapability(c.Model)
		require.False(t, ok)
	}
	c := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
	c.UpstreamModel = "Vendor/ExplicitRoute"
	account := videoProtocolAccount(73, videoprovider.ProviderZhiqiV1, c)
	require.ErrorContains(t, ValidateVideoTasksAccount(account), "differs from account mapping")
	_, ok := account.VideoModelCapability(c.Model)
	require.False(t, ok)
	account.Credentials["model_mapping"] = map[string]any{c.Model: c.UpstreamModel}
	require.NoError(t, ValidateVideoTasksAccount(account))
	require.True(t, account.IsModelSupported(c.Model))
}

func TestZhiqiAccountMappedCapabilityTakesPrecedenceOverPublicPreset(t *testing.T) {
	c := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
	c.Model = "Supplier/PrivateRoute"
	account := videoProtocolAccount(76, videoprovider.ProviderZhiqiV1, c)
	account.Credentials["model_mapping"] = map[string]any{"grok-imagine-video": c.Model}
	require.NoError(t, ValidateVideoTasksAccount(account))
	resolved, ok := account.VideoModelCapability("grok-imagine-video")
	require.True(t, ok)
	require.Equal(t, "grok-imagine-video", resolved.Model)
	require.Equal(t, c.Model, resolved.UpstreamModel)
	require.Equal(t, videoprovider.ProfileZhiqiV1, resolved.Profile)
	require.True(t, account.IsModelSupported("grok-imagine-video"))
	c.Enabled = false
	account.Extra[VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{c}
	require.False(t, account.IsModelSupported("grok-imagine-video"), "disabled mapped capability must not fall back to a preset")
	c.Enabled = true
	public := c
	public.Model, public.Enabled = "grok-imagine-video", false
	account.Extra[VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{c, public}
	require.False(t, account.IsModelSupported("grok-imagine-video"), "explicit public disable takes precedence over mapped capabilities")
}

func TestZhiqiDiscoveryReadsLiveModelIDsWithoutLLMMetadataOrAccountEdits(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"NewVendor/Video"},{"id":"OtherVideo"}]}`, `{"data":[]}`} {
		upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(body))}}}
		repo := &upstreamModelMetadataRepoStub{}
		svc := &AccountTestService{httpUpstream: upstream, accountRepo: repo, cfg: upstreamModelSyncTestConfig()}
		account := videoProtocolAccount(74, videoprovider.ProviderZhiqiV1, videoProtocolCapability(videoprovider.ProviderZhiqiV1))
		delete(account.Credentials, "base_url")
		before, err := json.Marshal(account)
		require.NoError(t, err)
		catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
		require.NoError(t, err)
		require.NotNil(t, catalog.Models)
		if body == `{"data":[]}` {
			require.Empty(t, catalog.Models, "deleted upstream routes must not be replaced with built-in templates")
		} else {
			require.Equal(t, []string{"NewVendor/Video", "OtherVideo"}, catalog.Models)
		}
		require.Empty(t, catalog.Metadata)
		require.Empty(t, catalog.Warnings)
		require.Nil(t, repo.updates)
		require.Len(t, upstream.requests, 1, "video discovery must not call an LLM metadata registry")
		require.Equal(t, "https://zhiqiapi.com/v1/models", upstream.requests[0].URL.String())
		after, err := json.Marshal(account)
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after), "live discovery must not authorize, price, or replace capabilities")
	}
}

func TestZhiqiPublicCatalogExposesOnlyCustomerModelAndCapabilities(t *testing.T) {
	c := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
	c.UpstreamModel = "Supplier/PrivateRoute"
	account := videoProtocolAccount(75, videoprovider.ProviderZhiqiV1, c)
	account.Credentials["api_key"] = "supplier-secret-never-expose"
	account.Credentials["base_url"] = "https://private-supplier.example/v1"
	account.Credentials["model_mapping"] = map[string]any{c.Model: c.UpstreamModel}
	account.Extra[AccountModelCostPricingExtraKey] = []ChannelModelPricing{explicitVideoPrice(c.UpstreamModel, VideoBillingPerVideo, 0.3)}
	svc, key := videoTasksGatewayPricingService(explicitVideoPrice(c.Model, VideoBillingPerVideo, 0.7))
	svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*account}}
	models, err := svc.ListVideoMediaModels(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, c.Model, models[0].ID)
	require.Len(t, models[0].Variants, 1)
	require.Equal(t, c.Resolutions, models[0].Variants[0].Resolutions)
	encoded, err := json.Marshal(models)
	require.NoError(t, err)
	for _, secret := range []string{c.UpstreamModel, "upstream_model", "private-supplier", "supplier-secret", "zhiqi_v1", "video_provider"} {
		require.NotContains(t, string(encoded), secret)
	}
}
