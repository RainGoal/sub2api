package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/stretchr/testify/require"
)

func TestWriteUsageLogKeepsFinalIDWhenAuditLinkCannotBeQueued(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(requestaudit.ConfigKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"retention_days":3,"version":1}`))
	manager := requestaudit.NewManager(db)
	require.NoError(t, manager.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	mock.ExpectClose()
	require.NoError(t, db.Close())
	ingress := requestaudit.BeginIngress(manager, requestaudit.RequestMeta{Kind: "http_request"})
	require.NotNil(t, ingress.Capture())
	ingress.BindIdentity(1, 12, 3)
	ctx := requestaudit.WithUsageLinkFromContext(context.Background(), requestaudit.WithIngress(context.Background(), ingress))
	ingress.Finish(200, time.Now(), "completed", nil)
	require.NoError(t, manager.Stop(context.Background()))
	for _, id := range []string{"client:http-request", "resp_ws_turn", "web_search:call"} {
		t.Run(id, func(t *testing.T) {
			repo := &openAIRecordUsageLogRepoStub{inserted: true}
			log := &UsageLog{RequestID: id, APIKeyID: 12}
			before := manager.Status().DroppedEvents
			writeUsageLogBestEffort(ctx, repo, log, "test.audit_link")
			require.Equal(t, before+1, manager.Status().DroppedEvents, "final usage attempts its audit association")
			require.Equal(t, 1, repo.calls, "audit failure must not affect usage persistence")
			require.Same(t, log, repo.lastLog)
			require.Equal(t, id, repo.lastLog.RequestID)
			require.NoError(t, repo.lastCtxErr)
		})
	}
	require.Zero(t, manager.Status().QueueBytes)
	require.NoError(t, mock.ExpectationsWereMet())
}
