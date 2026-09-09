package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayModelsSeedanceRespectsAccountSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID int64 = 981
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{
		byGroup: map[int64][]service.Account{
			groupID: {{
				ID: 1, Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{
					"video_provider": "fflink_v1",
					"model_mapping": map[string]any{
						"seedance-2.0-fast": "seedance-2.0-fast",
						"seedance-2.5":      "seedance-2.5",
					},
				},
			}},
		},
	})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformSeedance},
	})
	h.Models(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var response gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Equal(t, []string{"seedance-2.0-fast", "seedance-2.5"}, modelIDsForTest(response.Data))
}
