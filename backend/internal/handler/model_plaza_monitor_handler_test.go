//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type plazaMonitorHandlerStub struct {
	modelPlazaMonitorBackend
	reads int
}

func (s *plazaMonitorHandlerStub) GetStatus(_ context.Context, id int64) (*service.ModelPlazaMonitorStatusResponse, error) {
	s.reads++
	return &service.ModelPlazaMonitorStatusResponse{
		Enabled: true,
		Items:   []service.ModelPlazaMonitorStatus{{GroupID: id, Platform: "openai", Model: "test-model", Status: "operational"}},
	}, nil
}

type plazaMonitorCatalogStub struct{ groups []service.PlazaGroup }

func (s plazaMonitorCatalogStub) ListGroups(context.Context) ([]service.PlazaGroup, error) {
	return s.groups, nil
}

type plazaMonitorVisibilityStub struct {
	allowed  map[int64]struct{}
	restrict bool
	err      error
}

func (s plazaMonitorVisibilityStub) GetUserGroupVisibility(context.Context, int64) (map[int64]struct{}, bool, error) {
	return s.allowed, s.restrict, s.err
}

type plazaMonitorRuntimeStub struct{ runtime service.ModelPlazaRuntime }

func (s plazaMonitorRuntimeStub) GetModelPlazaRuntime(context.Context) service.ModelPlazaRuntime {
	return s.runtime
}

func TestModelPlazaMonitorStatusVisibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name        string
		query       string
		authed      bool
		requireAuth bool
		enabled     bool
		scope       plazaMonitorVisibilityStub
		want        int
		wantReads   int
	}{
		{name: "anonymous public", query: "1", enabled: true, want: 200, wantReads: 1},
		{name: "anonymous exclusive", query: "2", enabled: true, want: 404},
		{name: "requires login", query: "1", enabled: true, requireAuth: true, want: 401},
		{name: "disabled catalogue", query: "1", want: 404},
		{name: "invalid id", query: "abc", enabled: true, want: 400},
		{name: "negative id", query: "-1", enabled: true, want: 400},
		{name: "missing group", query: "99", enabled: true, want: 404},
		{name: "restricted public", query: "1", enabled: true, authed: true, scope: plazaMonitorVisibilityStub{allowed: map[int64]struct{}{}, restrict: true}, want: 404},
		{name: "exclusive granted", query: "2", enabled: true, authed: true, scope: plazaMonitorVisibilityStub{allowed: map[int64]struct{}{2: {}}}, want: 200, wantReads: 1},
		{name: "exclusive denied", query: "2", enabled: true, authed: true, scope: plazaMonitorVisibilityStub{allowed: map[int64]struct{}{}}, want: 404},
		{name: "authorization fails closed", query: "1", enabled: true, authed: true, scope: plazaMonitorVisibilityStub{err: errors.New("scope unavailable")}, want: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitor := &plazaMonitorHandlerStub{}
			h := &ModelPlazaMonitorHandler{
				monitor:  monitor,
				catalog:  plazaMonitorCatalogStub{groups: plazaGroups()},
				keys:     tc.scope,
				settings: plazaMonitorRuntimeStub{runtime: service.ModelPlazaRuntime{Enabled: tc.enabled, RequireAuth: tc.requireAuth}},
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza/status?group_id="+tc.query, nil)
			if tc.authed {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			}
			h.Status(c)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			require.Equal(t, tc.wantReads, monitor.reads)
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			if tc.want == 200 {
				require.Contains(t, w.Body.String(), `"model":"test-model"`)
				require.NotContains(t, w.Body.String(), "api_key")
			}
		})
	}
}

func TestModelPlazaMonitorStatusUninitializedFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza/status?group_id=1", nil)
	(&ModelPlazaMonitorHandler{}).Status(c)
	require.Equal(t, http.StatusNotFound, w.Code)
}
