package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type balanceAdminStub struct {
	saved     *service.UpstreamBalanceConfig
	refreshID string
	err       error
}

func (s *balanceAdminStub) List(context.Context) (*service.UpstreamBalanceList, error) {
	return &service.UpstreamBalanceList{}, s.err
}
func (s *balanceAdminStub) Discover(context.Context) (*service.UpstreamBalanceDiscovery, error) {
	return &service.UpstreamBalanceDiscovery{}, s.err
}
func (s *balanceAdminStub) SaveConfig(_ context.Context, config *service.UpstreamBalanceConfig) (*service.UpstreamBalanceConfig, error) {
	s.saved = config
	return config, s.err
}
func (s *balanceAdminStub) Refresh(_ context.Context, id string) (*service.UpstreamBalanceItem, error) {
	s.refreshID = id
	return &service.UpstreamBalanceItem{}, s.err
}

func balanceHandlerRequest(h *UpstreamBalanceHandler, method, path, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", h.List)
	router.GET("/discovery", h.Discover)
	router.PUT("/config", h.SaveConfig)
	router.POST("/:id/refresh", h.Refresh)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

func TestUpstreamBalanceHandlerRejectsInvalidInputBeforeService(t *testing.T) {
	stub := &balanceAdminStub{}
	h := &UpstreamBalanceHandler{service: stub}
	for _, body := range []string{`{`, `{"interval_minutes":"30"}`, `{"interval_seconds":15.5}`, `{"interval_seconds":"15"}`, `{"interval_seconds":null}`, strings.Repeat(" ", 256<<10) + `{}`} {
		w := balanceHandlerRequest(h, http.MethodPut, "/config", body)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Nil(t, stub.saved)
	}
	w := balanceHandlerRequest(h, http.MethodPost, "/invalid/refresh", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Empty(t, stub.refreshID)
}

func TestUpstreamBalanceHandlerPreservesConfigRevisionAndErrorContract(t *testing.T) {
	stub := &balanceAdminStub{}
	h := &UpstreamBalanceHandler{service: stub}
	w := balanceHandlerRequest(h, http.MethodPut, "/config", `{"version":7,"enabled":false,"interval_minutes":30,"wallets":[]}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.EqualValues(t, 7, stub.saved.Version)
	require.False(t, stub.saved.Enabled)
	var result struct {
		Code int                           `json:"code"`
		Data service.UpstreamBalanceConfig `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Zero(t, result.Code)
	require.EqualValues(t, 7, result.Data.Version)

	stub.err = infraerrors.Conflict("UPSTREAM_BALANCE_CONFIG_CONFLICT", "Configuration changed; reload and retry")
	w = balanceHandlerRequest(h, http.MethodPut, "/config", `{"version":7,"wallets":[]}`)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), `"reason":"UPSTREAM_BALANCE_CONFIG_CONFLICT"`)
}

func TestUpstreamBalanceHandlerReadAndRefreshResponsesAreNotCacheable(t *testing.T) {
	stub := &balanceAdminStub{}
	h := &UpstreamBalanceHandler{service: stub}
	const walletID = "626cf342-4208-49ae-8bc3-47eb48aba1ec"
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/discovery"},
		{http.MethodPost, "/" + walletID + "/refresh"},
	} {
		w := balanceHandlerRequest(h, endpoint.method, endpoint.path, "")
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	require.Equal(t, walletID, stub.refreshID)
}

func TestUpstreamBalanceHandlerAcceptsSecondAndLegacyMinuteContracts(t *testing.T) {
	for _, tc := range []struct {
		body             string
		seconds, minutes int
	}{
		{`{"version":7,"interval_seconds":15,"wallets":[]}`, 15, 0},
		{`{"version":7,"interval_seconds":15,"interval_minutes":30,"wallets":[]}`, 15, 30},
		{`{"version":7,"interval_minutes":7,"wallets":[]}`, 0, 7},
	} {
		stub := &balanceAdminStub{}
		w := balanceHandlerRequest(&UpstreamBalanceHandler{service: stub}, http.MethodPut, "/config", tc.body)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, tc.seconds, stub.saved.IntervalSeconds)
		require.Equal(t, tc.minutes, stub.saved.IntervalMinutes)
	}
}
