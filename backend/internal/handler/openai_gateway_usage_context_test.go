package handler

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/stretchr/testify/require"
)

func TestSubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "request-456")

	var gotClientRequestID string
	var gotRequestID string
	h := &GatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	})

	require.Equal(t, "client-request-123", gotClientRequestID)
	require.Equal(t, "request-456", gotRequestID)
}

func TestOpenAISubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxkey.ClientRequestID, "openai-client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "openai-request-456")

	var gotClientRequestID string
	var gotRequestID string
	h := &OpenAIGatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	})

	require.Equal(t, "openai-client-request-123", gotClientRequestID)
	require.Equal(t, "openai-request-456", gotRequestID)
}

func TestUsageRecordContextRetainsOnlyAuditLinkAfterRequestEnds(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(requestaudit.ConfigKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"retention_days":3,"version":1}`))
	manager := requestaudit.NewManager(db)
	require.NoError(t, manager.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	// Exercise an unavailable audit sink without adding a test-only callback to
	// production. A closed queue counts the late association as a dropped event.
	mock.ExpectClose()
	require.NoError(t, db.Close())
	ingress := requestaudit.BeginIngress(manager, requestaudit.RequestMeta{Kind: "websocket_handshake"})
	require.NotNil(t, ingress.Capture())
	ingress.BindIdentity(1, 12, 3)
	type largePayloadKey struct{}
	parent := requestaudit.WithIngress(context.WithValue(context.Background(), largePayloadKey{}, []byte("request body")), ingress)
	parent, cancel := context.WithCancel(parent)
	worker := usageRecordContext(parent, context.Background())
	cancel()
	ingress.Finish(101, time.Now(), "completed", nil)
	require.NoError(t, manager.Stop(context.Background()))
	require.NoError(t, worker.Err())
	require.Nil(t, worker.Value(largePayloadKey{}))
	require.Nil(t, requestaudit.IngressFromContext(worker))
	before := manager.Status().DroppedEvents
	requestaudit.LinkUsageRequest(worker, "resp_late_turn", 12)
	require.Equal(t, before+1, manager.Status().DroppedEvents, "the copied token still identifies the captured entry")
	require.Zero(t, manager.Status().QueueBytes)
	require.NoError(t, mock.ExpectationsWereMet())
}
