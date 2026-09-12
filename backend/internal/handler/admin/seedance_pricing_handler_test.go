package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type seedanceImportGroupRepo struct {
	service.GroupRepository
	groups map[int64]*service.Group
	reads  int
}

func (r *seedanceImportGroupRepo) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	r.reads++
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, service.ErrGroupNotFound
}

func seedanceImportRouter(repo *seedanceImportGroupRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewChannelHandler(service.NewChannelService(nil, repo, nil, nil, nil), nil, nil)
	router.POST("/api/v1/admin/channels/seedance-pricing/import-preview", handler.PreviewSeedancePricingImport)
	return router
}

func seedancePricingRequest(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestSeedancePricingImportPreviewHTTP(t *testing.T) {
	repo := &seedanceImportGroupRepo{groups: map[int64]*service.Group{
		7: {ID: 7, Platform: service.PlatformSeedance, VideoModelPrices: map[string]map[string]float64{"seedance-2.0": {"720p": 0.25}}},
	}}
	result := seedancePricingRequest(seedanceImportRouter(repo), http.MethodPost, "/api/v1/admin/channels/seedance-pricing/import-preview", `{"group_ids":[7]}`)
	require.Equal(t, http.StatusOK, result.Code, result.Body.String())
	var response struct {
		Data struct {
			ModelPricing []channelModelPricingResponse `json:"model_pricing"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &response))
	require.Len(t, response.Data.ModelPricing, 1)
	row := response.Data.ModelPricing[0]
	require.Equal(t, "video", row.BillingMode)
	require.Equal(t, service.PlatformSeedance, row.Platform)
	require.Equal(t, []string{"seedance-2.0"}, row.Models)
	require.Len(t, row.Intervals, 1)
	require.Equal(t, "720p", row.Intervals[0].TierLabel)
	require.Equal(t, 0.25, *row.Intervals[0].PerRequestPrice)
	require.Equal(t, 1, repo.reads)
	require.Equal(t, 0.25, repo.groups[7].VideoModelPrices["seedance-2.0"]["720p"])
}

func TestSeedancePricingImportPreviewRejectsInvalidSelection(t *testing.T) {
	for _, body := range []string{`{}`, `{"group_ids":[]}`, `{"group_ids":null}`, `{"group_ids":[0]}`, `{"group_ids":[-1]}`, `{"group_ids":["7"]}`, `{`} {
		t.Run(body, func(t *testing.T) {
			repo := &seedanceImportGroupRepo{}
			result := seedancePricingRequest(seedanceImportRouter(repo), http.MethodPost, "/api/v1/admin/channels/seedance-pricing/import-preview", body)
			require.Equal(t, http.StatusBadRequest, result.Code)
			require.Zero(t, repo.reads)
		})
	}
}

func TestSeedancePricingImportPreviewRejectsConflictingGroups(t *testing.T) {
	repo := &seedanceImportGroupRepo{groups: map[int64]*service.Group{
		7: {ID: 7, Platform: service.PlatformSeedance, VideoModelPrices: map[string]map[string]float64{"seedance-2.0": {"720p": 0.25}}},
		8: {ID: 8, Platform: service.PlatformSeedance, VideoModelPrices: map[string]map[string]float64{"seedance-2.0": {"720p": 0.5}}},
	}}
	result := seedancePricingRequest(seedanceImportRouter(repo), http.MethodPost, "/api/v1/admin/channels/seedance-pricing/import-preview", `{"group_ids":[7,8]}`)
	require.Equal(t, http.StatusConflict, result.Code)
	require.Contains(t, result.Body.String(), "SEEDANCE_LEGACY_PRICING_CONFLICT")
	require.Equal(t, 0.25, repo.groups[7].VideoModelPrices["seedance-2.0"]["720p"])
	require.Equal(t, 0.5, repo.groups[8].VideoModelPrices["seedance-2.0"]["720p"])
}

func TestSeedancePricingImportPreviewEmptyAndMissingGroups(t *testing.T) {
	repo := &seedanceImportGroupRepo{groups: map[int64]*service.Group{7: {ID: 7, Platform: service.PlatformSeedance}}}
	router := seedanceImportRouter(repo)
	result := seedancePricingRequest(router, http.MethodPost, "/api/v1/admin/channels/seedance-pricing/import-preview", `{"group_ids":[7]}`)
	require.Equal(t, http.StatusOK, result.Code)
	require.Contains(t, result.Body.String(), `"model_pricing":[]`)
	result = seedancePricingRequest(router, http.MethodPost, "/api/v1/admin/channels/seedance-pricing/import-preview", `{"group_ids":[99]}`)
	require.Equal(t, http.StatusNotFound, result.Code)
}

type seedanceSalesHandlerChannelRepo struct {
	service.ChannelRepository
	channel *service.Channel
}

func (r *seedanceSalesHandlerChannelRepo) ExistsByName(context.Context, string) (bool, error) {
	return false, nil
}
func (r *seedanceSalesHandlerChannelRepo) Create(_ context.Context, channel *service.Channel) error {
	channel.ID = 1
	r.channel = channel.Clone()
	return nil
}
func (r *seedanceSalesHandlerChannelRepo) GetByID(context.Context, int64) (*service.Channel, error) {
	return r.channel.Clone(), nil
}
func (r *seedanceSalesHandlerChannelRepo) Update(_ context.Context, channel *service.Channel) error {
	r.channel = channel.Clone()
	return nil
}
func (r *seedanceSalesHandlerChannelRepo) ListAll(context.Context) ([]service.Channel, error) {
	return []service.Channel{*r.channel.Clone()}, nil
}

func TestSeedanceSalesPriceRealChannelHTTPCreateAndUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &seedanceSalesHandlerChannelRepo{}
	handler := NewChannelHandler(service.NewChannelService(repo, nil, nil, nil, nil), nil, nil)
	router := gin.New()
	router.POST("/api/v1/admin/channels", handler.Create)
	router.PUT("/api/v1/admin/channels/:id", handler.Update)
	create := seedancePricingRequest(router, http.MethodPost, "/api/v1/admin/channels", `{
		"name":"seedance", "model_pricing":[{"platform":"seedance", "models":["bytedance/seedance-2.5"],
		"billing_mode":"video", "intervals":[{"tier_label":"720p", "per_request_price":0.25}]}]
	}`)
	require.Equal(t, http.StatusOK, create.Code, create.Body.String())
	require.Equal(t, service.BillingModeVideo, repo.channel.ModelPricing[0].BillingMode)
	require.Equal(t, []string{"seedance-2.5"}, repo.channel.ModelPricing[0].Models)
	require.Equal(t, true, repo.channel.FeaturesConfig["seedance_sales_pricing"])
	update := seedancePricingRequest(router, http.MethodPut, "/api/v1/admin/channels/1", `{
		"model_pricing":[{"platform":"seedance", "models":["seedance-2.5"], "billing_mode":"video", "per_request_price":0.5}]
	}`)
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())
	require.Equal(t, 0.5, *repo.channel.ModelPricing[0].PerRequestPrice)
}

func TestSeedanceSalesPriceRequestBindingAcceptsVideo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/binding", func(c *gin.Context) {
		var request channelModelPricingRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusNoContent)
	})
	accepted := seedancePricingRequest(router, http.MethodPost, "/binding", `{"platform":"seedance","models":["seedance-2.0"],"billing_mode":"video"}`)
	require.Equal(t, http.StatusNoContent, accepted.Code)
	rejected := seedancePricingRequest(router, http.MethodPost, "/binding", `{"platform":"seedance","models":["seedance-2.0"],"billing_mode":"unsupported"}`)
	require.Equal(t, http.StatusBadRequest, rejected.Code)
}
