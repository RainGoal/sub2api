package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Keep the public Seedance aliases explicit.  The handlers are selected at
// runtime from the API-key group, so a route can silently regress to a 404
// without any compile-time signal.
func TestGatewayRoutesSeedanceVideoAliasMatrixIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformSeedance)
	registered := make(map[string]struct{})
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}

	want := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/videos"},
		{http.MethodPost, "/v1/videos/generations"},
		{http.MethodGet, "/v1/videos/:request_id"},
		{http.MethodGet, "/v1/videos/:request_id/content"},
		{http.MethodGet, "/v1/videos/generations/:request_id"},
		{http.MethodGet, "/v1/videos/generations/:request_id/content"},
		{http.MethodGet, "/v1/videos/jobs/:request_id"},
		{http.MethodGet, "/v1/videos/jobs/:request_id/content"},
		{http.MethodDelete, "/v1/videos/jobs/:request_id"},
		{http.MethodPost, "/videos"},
		{http.MethodPost, "/videos/generations"},
		{http.MethodGet, "/videos/:request_id"},
		{http.MethodGet, "/videos/:request_id/content"},
		{http.MethodGet, "/videos/generations/:request_id"},
		{http.MethodGet, "/videos/generations/:request_id/content"},
		{http.MethodGet, "/videos/jobs/:request_id"},
		{http.MethodGet, "/videos/jobs/:request_id/content"},
		{http.MethodDelete, "/videos/jobs/:request_id"},
	}
	for _, route := range want {
		require.Contains(t, registered, route.method+" "+route.path)
	}
}
