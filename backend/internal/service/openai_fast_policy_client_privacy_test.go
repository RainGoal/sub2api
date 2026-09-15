package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIFastPolicyClientPrivacyPreservesBlockingAndOriginalCause(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			c, rec := newOpenAIUpstreamErrorTestContext(t)
			blocked := &OpenAIFastBlockedError{Message: "priority is blocked for internal-C"}
			writeOpenAIFastPolicyBlockedResponse(c, blocked, &Account{Platform: platform})
			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Equal(t, "permission_error", gjson.Get(rec.Body.String(), "error.type").String())
			require.True(t, HasOpsClientBusinessLimited(c))
			require.Equal(t, "priority is blocked for internal-C", blocked.Message)
			if platform == PlatformOpenAI {
				require.Equal(t, "Request blocked by policy.", gjson.Get(rec.Body.String(), "error.message").String())
				require.NotContains(t, rec.Body.String(), "internal-C")
			} else {
				require.Contains(t, rec.Body.String(), "internal-C")
			}
		})
	}
}

func TestOpenAIFastPolicyClientPrivacyAfterKeepaliveCommit(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	waitForKeepaliveBeats()
	blocked := &OpenAIFastBlockedError{Message: "priority is blocked for internal-C"}
	writeOpenAIFastPolicyBlockedResponse(c, blocked, &Account{Platform: PlatformOpenAI})
	require.Equal(t, http.StatusOK, rec.Code)
	events := parseCompactBridgeSSE(t, stripKeepaliveComments(rec.Body.String()))
	require.Len(t, events, 1)
	require.Equal(t, "response.failed", events[0][0])
	require.Equal(t, "permission_error", gjson.Get(events[0][1], "response.error.code").String())
	require.Equal(t, "Request blocked by policy.", gjson.Get(events[0][1], "response.error.message").String())
	require.NotContains(t, rec.Body.String(), "internal-C")
	require.Equal(t, "priority is blocked for internal-C", blocked.Message)
}
