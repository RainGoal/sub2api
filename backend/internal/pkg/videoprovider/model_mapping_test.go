package videoprovider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLegacyVideoEncoderUsesResolvedModelWithoutChangingPublicIdentity(t *testing.T) {
	for _, provider := range []ID{ProviderBBLabuV1, ProviderFFLinkV1} {
		driver, err := Resolve(string(provider))
		require.NoError(t, err)
		for _, upstream := range []string{"Seedance-2.0", "seedance-2.0"} {
			request := &CreateRequest{RequestedModel: "studio-video", Model: ModelSeedance20,
				UpstreamModel: upstream, Prompt: "waves", Duration: 6, Resolution: "720p", AspectRatio: "16:9"}
			wire, err := driver.BuildRequest(context.Background(), RequestParams{
				BaseURL: driver.DefaultBaseURL(), APIKey: "test-key", Operation: OperationCreate, CreateRequest: request})
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(requestBody(t, wire), &payload))
			require.Equal(t, upstream, payload["model"])
			require.Equal(t, "studio-video", request.RequestedModel)
			request.UpstreamModel = ModelSeedance25
			_, err = driver.BuildRequest(context.Background(), RequestParams{
				BaseURL: driver.DefaultBaseURL(), APIKey: "test-key", Operation: OperationCreate, CreateRequest: request})
			require.ErrorContains(t, err, "differs from the validated model")
		}
	}
}

func TestLegacyVideoDefaultWireModelsMatchResolver(t *testing.T) {
	for _, provider := range []ID{ProviderBBLabuV1, ProviderFFLinkV1} {
		for _, public := range []string{"seedance-2.0", "Seedance-2.5", "bytedance/seedance-2.5"} {
			driver, err := Resolve(string(provider))
			require.NoError(t, err)
			request, _, err := ParseCreateRequest([]byte(`{"model":"` + public + `","prompt":"waves","duration":6,"resolution":"720p"}`))
			require.NoError(t, err)
			wire, err := driver.BuildRequest(context.Background(), RequestParams{
				BaseURL: driver.DefaultBaseURL(), APIKey: "test-key", Operation: OperationCreate, CreateRequest: request})
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(requestBody(t, wire), &payload))
			expected, err := ResolveLegacyUpstreamModel(provider, public)
			require.NoError(t, err)
			require.Equal(t, expected, payload["model"])
		}
	}
}

func TestVideoCanonicalRequestExcludesResolvedSupplierModel(t *testing.T) {
	request := &CreateRequest{RequestedModel: "studio-video", Model: ModelSeedance20,
		Prompt: "waves", UpstreamModel: "Seedance-2.0"}
	first, err := request.CanonicalJSON()
	require.NoError(t, err)
	request.UpstreamModel = "seedance-2.0"
	second, err := request.CanonicalJSON()
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NotContains(t, string(first), "upstream_model")
}
