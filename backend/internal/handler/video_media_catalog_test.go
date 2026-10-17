package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoMediaCatalogRequiresAPIKeyAndReturnsStableEmptyList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
	for _, authenticated := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/media/models", nil)
		if authenticated {
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1})
		}
		h.MediaModels(c)
		if !authenticated {
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			continue
		}
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		catalog, prices := service.VideoMediaCatalogVersions([]service.VideoMediaModel{})
		require.JSONEq(t, `{"object":"list","schema_version":1,"catalog_version":"`+catalog+`","price_version":"`+prices+`","data":[]}`, recorder.Body.String())
	}
}

func TestVideoTasksReviewStatusNeverPollsOrExposesProviderIdentity(t *testing.T) {
	pending := &service.SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff", TaskID: "supplier-secret-task",
		Model: "omni-fast", SettlementStatus: service.SeedanceVideoSettlementNeedsReview}
	result, local := seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, pending.PublicID(), pending)
	require.True(t, local)
	response := service.BuildSeedanceVideoResponse(result, seedanceVideoResponseMeta(pending.PublicID(), result, pending, service.SeedanceVideoRequestInfo{}))
	require.Equal(t, pending.PublicID(), response.ID)
	require.Equal(t, service.SeedanceVideoResponseStatusInProgress, response.Status)
	require.Empty(t, response.Resolution)
	require.Zero(t, response.Duration)
	require.Nil(t, response.ContentURL)
}
