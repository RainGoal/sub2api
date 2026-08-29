package middleware

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEveryGatewayPOSTRouteIsClassifiedForConversationAudit(t *testing.T) {
	routeSource, err := os.ReadFile(filepath.Join("..", "routes", "gateway.go"))
	require.NoError(t, err)

	pattern := regexp.MustCompile(`(gateway|gemini|r|codexDirect|antigravityV1|antigravityV1Beta)\.POST\("([^"]+)"`)
	matches := pattern.FindAllStringSubmatch(string(routeSource), -1)
	require.NotEmpty(t, matches)

	for _, match := range matches {
		receiver, route := match[1], match[2]
		path := conversationAuditCoveragePath(receiver, route)
		wantCaptured := conversationAuditCoverageExpected(route)
		_, captured := classifyConversationAuditRoute(http.MethodPost, path)
		require.Equalf(t, wantCaptured, captured, "POST %s from %s.%s", path, receiver, route)
	}
}

func conversationAuditCoveragePath(receiver, route string) string {
	prefix := map[string]string{
		"gateway":           "/v1",
		"gemini":            "/v1beta",
		"r":                 "",
		"codexDirect":       "/backend-api/codex",
		"antigravityV1":     "/antigravity/v1",
		"antigravityV1Beta": "/antigravity/v1beta",
	}[receiver]

	route = strings.ReplaceAll(route, "*subpath", "compact")
	route = strings.ReplaceAll(route, "*modelAction", "gpt-test:generateContent")
	route = strings.ReplaceAll(route, ":id", "batch-1")
	return prefix + route
}

func conversationAuditCoverageExpected(route string) bool {
	switch {
	case strings.Contains(route, "/messages/count_tokens"):
		return false
	case strings.Contains(route, "/images/batches/") && strings.HasSuffix(route, "/cancel"):
		return false
	case route == "/custom-voices":
		return false
	default:
		return true
	}
}
