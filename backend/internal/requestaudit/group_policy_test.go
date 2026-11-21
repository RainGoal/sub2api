package requestaudit

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGroupPolicyRequiresStableEncryptionKey(t *testing.T) {
	manager := NewManager(nil)
	_, err := manager.UpdateGroupPolicies(context.Background(), GroupPolicyConfig{
		GroupOnly: true,
		Rules:     []GroupPolicy{{GroupID: 4, UpstreamRequestHeaders: true}},
	}, 1)
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "UPSTREAM_AUDIT_KEY_REQUIRED", auditErr.Code)
}

func TestGroupCaptureRequiresAuthenticatedMatchingGroup(t *testing.T) {
	manager := captureTestManager()
	manager.cacheGroupPolicies(GroupPolicyConfig{GroupOnly: true, Rules: []GroupPolicy{{GroupID: 4, InboundHeaders: true}}, Version: 2})
	reads := 0
	headers := func() http.Header {
		reads++
		return http.Header{"X-Group-Test": {"first", "second"}}
	}
	for _, groupID := range []int64{0, 3, 4} {
		ingress, pending := BeginIngressWithHeaders(manager, RequestMeta{}, headers)
		require.True(t, pending)
		require.Nil(t, ingress.Capture())
		capture, captureBody := ingress.ActivateGroupCapture(groupID, headers)
		require.False(t, captureBody)
		ingress.BindIdentity(10, 20, groupID)
		if groupID != 4 {
			require.Nil(t, capture)
			require.Empty(t, manager.queue)
			continue
		}
		require.NotNil(t, capture)
		require.EqualValues(t, 4, ingress.Identity().GroupID)
		ingress.BindIdentity(10, 20, 8)
		require.EqualValues(t, 4, ingress.Identity().GroupID)
		ingress.Finish(http.StatusOK, time.Now(), "completed", nil)
		var payload []byte
		for _, event := range takeEvents(manager) {
			if event.kind == "header_snapshot" {
				require.NoError(t, materializeHeaderSnapshot(&event))
			}
			if event.kind == "chunk" {
				require.Equal(t, PartHeaders, event.part)
				payload = append(payload, event.raw...)
			}
		}
		require.Contains(t, string(payload), `"X-Group-Test":["first","second"]`)
	}
	require.Equal(t, 1, reads)
}

func TestWebSocketFramesUseCurrentGroupPolicy(t *testing.T) {
	manager := captureTestManager()
	manager.cacheGroupPolicies(GroupPolicyConfig{GroupOnly: true, Rules: []GroupPolicy{{GroupID: 4, InboundBody: true}}, Version: 2})
	ingress := BeginIngress(manager, RequestMeta{Kind: "websocket_handshake"})
	capture, _ := ingress.ActivateGroupCapture(4, nil)
	require.NotNil(t, capture)
	ingress.BindIdentity(10, 20, 4)
	manager.releaseEvents(takeEvents(manager))
	ingress.RecordMessage("text", []byte("first frame"))
	events := takeEvents(manager)
	require.NotEmpty(t, events)
	manager.releaseEvents(events)

	manager.cacheGroupPolicies(GroupPolicyConfig{GroupOnly: true, Rules: []GroupPolicy{}, Version: 3})
	ingress.RecordMessage("text", []byte("after removing the rule"))
	require.Empty(t, manager.queue)
}
