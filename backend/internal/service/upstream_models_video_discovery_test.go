package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestSyncVideoDiscoveryEmptyAndInvalidLists(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		status int
		valid  bool
	}{
		{"empty catalog", `{"data":[]}`, http.StatusOK, true},
		{"missing data", `{}`, http.StatusOK, false},
		{"null data", `{"data":null}`, http.StatusOK, false},
		{"error envelope", `{"error":{"message":"secret"}}`, http.StatusOK, false},
		{"partial catalog", `{"data":[{"id":"Brand/NewVideo"},{}]}`, http.StatusOK, false},
		{"not found", `{"error":"not found"}`, http.StatusNotFound, false},
		{"method not allowed", `{"error":"not supported"}`, http.StatusMethodNotAllowed, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: test.status,
				Body: io.NopCloser(strings.NewReader(test.body))}}}
			repo := &upstreamModelMetadataRepoStub{}
			svc := &AccountTestService{httpUpstream: upstream, accountRepo: repo, cfg: upstreamModelSyncTestConfig()}
			capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
			require.True(t, ok)
			account := &Account{ID: 999, Platform: PlatformSeedance, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "key", "base_url": "https://video.example/v1",
					"video_provider": string(videoprovider.ProviderVideoTasksJSONV1),
					"model_mapping":  map[string]any{"Seedance-2.0": "Seedance-2.0"}},
				Extra: map[string]any{VideoModelCapabilitiesExtra: []videoprovider.VideoModelCapability{capability}}}
			before, err := json.Marshal(account)
			require.NoError(t, err)
			catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
			if test.valid {
				require.NoError(t, err)
				require.NotNil(t, catalog.Models)
				require.Empty(t, catalog.Models)
			} else {
				require.Error(t, err)
				require.Nil(t, catalog, "failed discovery must not return configured models as a live list")
				var syncErr *UpstreamModelSyncError
				require.True(t, errors.As(err, &syncErr))
				require.Equal(t, UpstreamModelSyncErrorUpstream, syncErr.Kind)
			}
			after, marshalErr := json.Marshal(account)
			require.NoError(t, marshalErr)
			require.JSONEq(t, string(before), string(after))
			require.Nil(t, repo.updates)
			require.Len(t, upstream.requests, 1, "video discovery must not query registry metadata")
		})
	}
}

func TestFetchLegacyVideoDiscoveryStillRejectsEmptyCatalog(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		t.Run(string(provider), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}}
			svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			models, err := svc.FetchUpstreamSupportedModels(context.Background(), &Account{
				Platform: PlatformSeedance, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "key", "base_url": "https://video.example/v1",
					"video_provider": string(provider)},
			})
			require.ErrorContains(t, err, "Upstream returned no supported models")
			require.Nil(t, models)
		})
	}
}
