package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSeedanceVideoTaskRead(t *testing.T) {
	for _, path := range []string{
		"/v1/videos/task-123",
		"/videos/task-123",
		"/v1/videos/generations/task-123",
		"/videos/jobs/task-123/content",
	} {
		require.True(t, isSeedanceVideoTaskRead(http.MethodGet, path, "seedance"), path)
	}
	for _, path := range []string{"/v1/videos/jobs/task-123", "/videos/jobs/task-123"} {
		require.True(t, isSeedanceVideoTaskRead(http.MethodDelete, path, "seedance"), path)
	}
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/videos/generations"},
		{http.MethodGet, "/v1/videos"},
		{http.MethodDelete, "/v1/videos/task-123"},
		{http.MethodGet, "/v1/images/tasks/task-123"},
	} {
		require.False(t, isSeedanceVideoTaskRead(test.method, test.path, "seedance"), "%s %s", test.method, test.path)
	}
	require.False(t, isSeedanceVideoTaskRead(http.MethodGet, "/v1/videos/task-123", "grok"))
	require.False(t, isSeedanceVideoTaskRead(http.MethodGet, "/v1/videos/task-123", ""))
}
