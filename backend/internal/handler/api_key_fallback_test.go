package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fallbackHandlerKeyRepo struct {
	service.APIKeyRepository
	key    *service.APIKey
	fields service.APIKeyUpdateFields
}

func (r *fallbackHandlerKeyRepo) Create(_ context.Context, key *service.APIKey) error {
	key.ID = 1
	r.key = key
	return nil
}
func (r *fallbackHandlerKeyRepo) GetByID(context.Context, int64) (*service.APIKey, error) {
	k := *r.key
	return &k, nil
}
func (r *fallbackHandlerKeyRepo) Update(_ context.Context, k *service.APIKey, fields service.APIKeyUpdateFields) error {
	r.key, r.fields = k, fields
	return nil
}

type fallbackManagementUserRepo struct{ service.UserRepository }

func (fallbackManagementUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 1, Status: service.StatusActive}, nil
}

type fallbackManagementGroupRepo struct{ service.GroupRepository }

func (fallbackManagementGroupRepo) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	return &service.Group{ID: id, Status: service.StatusActive, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard}, nil
}
func (r fallbackManagementGroupRepo) GetByID(ctx context.Context, id int64) (*service.Group, error) {
	return r.GetByIDLite(ctx, id)
}

func TestAPIKeyFallbackHTTPCreateUpdate(t *testing.T) {
	repo := &fallbackHandlerKeyRepo{}
	cfg := &config.Config{}
	keys := service.NewAPIKeyService(repo, fallbackManagementUserRepo{}, fallbackManagementGroupRepo{}, nil, nil, nil, cfg)
	h := NewAPIKeyHandler(keys, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	router.POST("/api/v1/keys", h.Create)
	router.PUT("/api/v1/keys/:id", h.Update)
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		var payload map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		return payload
	}
	created := request(http.MethodPost, "/api/v1/keys", `{"name":"test","group_id":1,"fallback_group_id":2}`, http.StatusOK)
	createdData, ok := created["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(2), createdData["fallback_group_id"])
	request(http.MethodPut, "/api/v1/keys/1", `{"name":"renamed"}`, http.StatusOK)
	require.Equal(t, int64(2), *repo.key.FallbackGroupID)
	request(http.MethodPut, "/api/v1/keys/1", `{"fallback_group_id":3}`, http.StatusOK)
	require.Equal(t, int64(3), *repo.key.FallbackGroupID)
	request(http.MethodPut, "/api/v1/keys/1", `{"fallback_group_id":1}`, http.StatusBadRequest)
	require.Equal(t, int64(3), *repo.key.FallbackGroupID)
	request(http.MethodPut, "/api/v1/keys/1", `{"fallback_group_id":null}`, http.StatusOK)
	require.Nil(t, repo.key.FallbackGroupID)
	require.Equal(t, service.APIKeyUpdateFields{FallbackGroupID: true}, repo.fields)
}
