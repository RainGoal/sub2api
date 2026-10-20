package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountHandlerVideoDiscoveryEmptyAndInvalidCatalogs(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		status int
		want   []string
	}{
		{"new model", `{"data":[{"id":"Brand/NewVideo"}]}`, http.StatusOK, []string{"Brand/NewVideo"}},
		{"empty catalog", `{"data":[]}`, http.StatusOK, []string{}},
		{"invalid catalog", `{}`, http.StatusBadGateway, nil},
		{"error envelope", `{"error":{"message":"upstream-secret"}}`, http.StatusBadGateway, nil},
	} {
		for _, preview := range []bool{false, true} {
			name := test.name
			if preview {
				name += "/preview"
			}
			t.Run(name, func(t *testing.T) {
				svc := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: service.Account{
					ID: 777, Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "key", "base_url": "https://video.example/v1",
						"video_provider": "video_tasks_json_v1", "model_mapping": map[string]any{"Seedance-2.0": "Seedance-2.0"}},
				}}
				upstream := &syncUpstreamHTTPUpstream{resp: &http.Response{StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(test.body))}}
				router := setupSyncUpstreamModelsRouter(svc, upstream)
				request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/777/models/sync-upstream", nil)
				if preview {
					request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/models/sync-upstream-preview",
						strings.NewReader(`{"platform":"seedance","type":"apikey","api_key":"key","base_url":"https://video.example/v1","video_provider":"video_tasks_json_v1"}`))
					request.Header.Set("Content-Type", "application/json")
				}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, request)
				require.Equal(t, test.status, rec.Code)
				require.Zero(t, svc.updateAccountCalls)
				require.Zero(t, svc.updateAccountExtraCalls)
				if test.status == http.StatusOK {
					var response struct {
						Data service.UpstreamModelCatalog `json:"data"`
					}
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
					require.Equal(t, test.want, response.Data.Models)
				} else {
					require.NotContains(t, rec.Body.String(), "upstream-secret")
				}
			})
		}
	}
}
