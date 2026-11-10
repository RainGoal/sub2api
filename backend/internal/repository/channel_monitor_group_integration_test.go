//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorGroupBindingRoundTripAndRename(t *testing.T) {
	ctx := context.Background()
	repo := NewChannelMonitorRepository(integrationEntClient, integrationDB)
	group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: fmt.Sprintf("monitor-group-%d", time.Now().UnixNano())})
	t.Cleanup(func() { _ = integrationEntClient.Group.DeleteOneID(group.ID).Exec(ctx) })
	monitor := &service.ChannelMonitor{
		Name: "bound-monitor", Provider: service.MonitorProviderOpenAI,
		Endpoint: "https://example.com", APIKey: "encrypted", PrimaryModel: "test-model",
		GroupID: &group.ID, GroupName: "do not copy", Enabled: true, IntervalSeconds: 60,
	}
	require.NoError(t, repo.Create(ctx, monitor))
	t.Cleanup(func() { _ = repo.Delete(ctx, monitor.ID) })
	require.Equal(t, group.Name, monitor.GroupName)
	stored, err := integrationEntClient.ChannelMonitor.Get(ctx, monitor.ID)
	require.NoError(t, err)
	require.Empty(t, stored.GroupName)

	renamed := group.Name + "-renamed"
	require.NoError(t, integrationEntClient.Group.UpdateOneID(group.ID).SetName(renamed).Exec(ctx))
	loaded, err := repo.GetByID(ctx, monitor.ID)
	require.NoError(t, err)
	require.Equal(t, renamed, loaded.GroupName)
	listed, total, err := repo.List(ctx, service.ChannelMonitorListParams{Search: renamed})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, renamed, listed[0].GroupName)
	enabled, err := repo.ListEnabled(ctx)
	require.NoError(t, err)
	var found bool
	for _, item := range enabled {
		if item.ID == monitor.ID {
			found = true
			require.Equal(t, renamed, item.GroupName)
			require.Equal(t, group.ID, *item.GroupID)
		}
	}
	require.True(t, found)

	loaded.GroupID = nil
	loaded.GroupName = ""
	require.NoError(t, repo.Update(ctx, loaded))
	loaded, err = repo.GetByID(ctx, monitor.ID)
	require.NoError(t, err)
	require.Nil(t, loaded.GroupID)
	require.Empty(t, loaded.GroupName)
}
