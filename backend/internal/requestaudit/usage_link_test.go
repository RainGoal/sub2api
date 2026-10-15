package requestaudit

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageLinkCopiesOnlyEntryTokenAndSurvivesFinish(t *testing.T) {
	type payloadKey struct{}
	m := captureTestManager()
	ingress := BeginIngress(m, RequestMeta{Kind: "websocket_handshake"})
	ingress.BindIdentity(1, 12, 3)
	parent := WithIngress(context.WithValue(context.Background(), payloadKey{}, []byte("do not retain")), ingress)
	parent, cancel := context.WithCancel(parent)
	usageContext := WithUsageLinkFromContext(context.Background(), parent)
	cancel()
	require.NoError(t, usageContext.Err())
	require.Nil(t, usageContext.Value(payloadKey{}))
	require.Nil(t, IngressFromContext(usageContext))
	token, ok := usageLinkFromContext(usageContext)
	require.True(t, ok)
	require.Same(t, m, token.manager)
	require.Equal(t, ingress.capture.id, token.auditID)
	require.Equal(t, int64(12), token.apiKeyID)
	ingress.Finish(101, time.Now(), "completed", nil)
	m.releaseEvents(takeEvents(m))
	LinkUsageRequest(usageContext, "resp_first", 12)
	LinkUsageRequest(parent, "resp_second", 12)
	events := takeEvents(m)
	require.Len(t, events, 2)
	for _, event := range events {
		require.Equal(t, "usage_link", event.kind)
		require.Equal(t, token.auditID, event.id)
		require.Equal(t, int64(12), event.usageAPIKeyID)
		require.Equal(t, eventOverhead+int64(len(event.usageRequestID)), event.queued)
	}
	require.Equal(t, "resp_first", events[0].usageRequestID)
	require.Equal(t, "resp_second", events[1].usageRequestID)
	m.releaseEvents(events)
	require.Zero(t, m.queueBytes.Load())
	require.Zero(t, m.queueEvents.Load())
}

func TestUsageLinkRequiresCaptureOriginalKeyAndValidID(t *testing.T) {
	m := captureTestManager()
	ingress := BeginIngress(m, RequestMeta{})
	ingress.BindIdentity(1, 12, 3)
	ctx := WithIngress(context.Background(), ingress)
	m.releaseEvents(takeEvents(m))
	for _, id := range []string{"", "bad\x00id", "bad\nid", "bad\x7fid", "bad\xffid", strings.Repeat("x", MaxRequestIDBytes+1)} {
		LinkUsageRequest(ctx, id, 12)
	}
	LinkUsageRequest(ctx, "resp_other_key", 13)
	LinkUsageRequest(ctx, "resp_invalid_key", 0)
	LinkUsageRequest(context.Background(), "resp_no_capture", 12)
	require.Empty(t, takeEvents(m))
	require.Zero(t, m.queueBytes.Load())
	m.config.Enabled = false
	disabled := BeginIngress(m, RequestMeta{})
	disabled.BindIdentity(1, 12, 3)
	disabledContext := WithIngress(context.Background(), disabled)
	base := context.Background()
	require.Equal(t, base, WithUsageLinkFromContext(base, disabledContext))
	LinkUsageRequest(disabledContext, "resp_capture_disabled", 12)
	require.Empty(t, takeEvents(m))
}

func TestUsageLinkQueuePressureRemainsNonBlockingAndAccounted(t *testing.T) {
	m := captureTestManager()
	ingress := BeginIngress(m, RequestMeta{})
	ingress.BindIdentity(1, 12, 3)
	ctx := WithIngress(context.Background(), ingress)
	m.releaseEvents(takeEvents(m))
	m.queue = make(chan auditEvent, 1)
	m.maxEvents = 1
	LinkUsageRequest(ctx, "resp_first", 12)
	LinkUsageRequest(ctx, "resp_dropped", 12)
	events := takeEvents(m)
	require.Len(t, events, 1)
	require.Equal(t, "resp_first", events[0].usageRequestID)
	require.EqualValues(t, 1, m.droppedEvents.Load())
	m.releaseEvents(events)
	require.Zero(t, m.queueBytes.Load())
	require.Zero(t, m.queueEvents.Load())
}

func TestPersistUsageLinksIsIdempotentAndIgnoresMissingParents(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	mock.ExpectBegin()
	expectRetention(mock)
	for _, affected := range []int64{1, 0, 0} {
		mock.ExpectExec(`INSERT INTO request_audit_usage_links`).WithArgs(id, "resp_final", int64(12), 3).
			WillReturnResult(sqlmock.NewResult(0, affected))
	}
	mock.ExpectCommit()
	event := auditEvent{kind: "usage_link", id: id, usageRequestID: "resp_final", usageAPIKeyID: 12}
	require.NoError(t, m.persistBatch(context.Background(), []auditEvent{event, event, event,
		{kind: "usage_link", id: id, usageRequestID: "bad\x00id", usageAPIKeyID: 12},
		{kind: "usage_link", id: id, usageRequestID: "resp_wrong_key", usageAPIKeyID: 0},
	}))
}
