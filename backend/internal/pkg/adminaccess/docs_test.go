package adminaccess

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDocsPermissionsDoNotGrantPublicationThroughDraftWrite(t *testing.T) {
	writer := Policy{GlobalPermissions: []string{"docs.read", "docs.write"}}
	reader := Policy{GlobalPermissions: []string{"docs.read"}}
	publisher := Policy{GlobalPermissions: []string{"docs.publish"}}
	require.NoError(t, ValidatePolicy(writer))
	for _, path := range []string{"/api/v1/admin/docs/tree", "/api/v1/admin/docs/pages/:id"} {
		require.True(t, RouteAllowed(reader, "GET", path))
		require.False(t, RouteAllowed(Policy{}, "GET", path))
	}
	require.True(t, RouteAllowed(writer, "PUT", "/api/v1/admin/docs/pages/:id/draft"))
	require.False(t, RouteAllowed(reader, "PUT", "/api/v1/admin/docs/pages/:id/draft"))
	for _, path := range []string{"/api/v1/admin/docs/pages/:id/publish", "/api/v1/admin/docs/pages/:id/unpublish"} {
		require.False(t, RouteAllowed(writer, "POST", path))
		require.True(t, RouteAllowed(publisher, "POST", path))
	}
	require.False(t, RouteAllowed(writer, "DELETE", "/api/v1/admin/docs/nodes/:id"))
}
