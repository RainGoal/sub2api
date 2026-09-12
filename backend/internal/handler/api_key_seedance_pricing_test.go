package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type seedanceGroupsUserRepo struct{ service.UserRepository }

func (seedanceGroupsUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 1}, nil
}

type seedanceGroupsRepo struct {
	service.GroupRepository
	groups []service.Group
}

func (r seedanceGroupsRepo) ListActive(context.Context) ([]service.Group, error) {
	return r.groups, nil
}

type seedanceGroupsSubscriptionRepo struct {
	service.UserSubscriptionRepository
}

func (seedanceGroupsSubscriptionRepo) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type seedanceGroupsChannelRepo struct {
	service.ChannelRepository
	err   error
	reads int
}

func (r *seedanceGroupsChannelRepo) ListAll(context.Context) ([]service.Channel, error) {
	r.reads++
	return nil, r.err
}

func seedanceGroupsRequest(t *testing.T, groups []service.Group, channels *seedanceGroupsChannelRepo) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keys := service.NewAPIKeyService(nil, seedanceGroupsUserRepo{}, seedanceGroupsRepo{groups: groups}, seedanceGroupsSubscriptionRepo{}, nil, nil, nil)
	h := NewAPIKeyHandler(keys, service.NewChannelService(channels, nil, nil, nil, nil))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	router.GET("/api/v1/groups/available", h.GetAvailableGroups)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/groups/available", nil))
	return response
}

func TestAvailableGroupsSeedancePricingFailureDoesNotBlockOtherPlatforms(t *testing.T) {
	channels := &seedanceGroupsChannelRepo{err: errors.New("channel configuration query failed")}
	legacy := 0.1
	response := seedanceGroupsRequest(t, []service.Group{
		{ID: 1, Platform: service.PlatformOpenAI, Status: service.StatusActive},
		{ID: 2, Platform: service.PlatformSeedance, Status: service.StatusActive, VideoPrice720P: &legacy,
			VideoModelPrices: map[string]map[string]float64{"seedance-2.0": {"720p": 0.1}}},
	}, channels)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"platform":"openai"`)
	var result struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Len(t, result.Data, 2)
	require.Equal(t, service.PlatformSeedance, result.Data[1]["platform"])
	require.Nil(t, result.Data[1]["video_model_prices"])
	require.Nil(t, result.Data[1]["video_price_720p"])
}

func TestAvailableGroupsWithoutSeedanceDoesNotReadChannels(t *testing.T) {
	channels := &seedanceGroupsChannelRepo{err: errors.New("channel configuration query failed")}
	response := seedanceGroupsRequest(t, []service.Group{{ID: 1, Platform: service.PlatformOpenAI, Status: service.StatusActive}}, channels)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Zero(t, channels.reads)
}

func TestAvailableGroupsUnmigratedSeedanceKeepsLegacyPrices(t *testing.T) {
	response := seedanceGroupsRequest(t, []service.Group{
		{ID: 2, Platform: service.PlatformSeedance, Status: service.StatusActive, VideoModelPrices: map[string]map[string]float64{"seedance-2.0": {"720p": 0.1}}},
	}, &seedanceGroupsChannelRepo{})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"video_model_prices":{"seedance-2.0":{"720p":0.1}}`)
}
