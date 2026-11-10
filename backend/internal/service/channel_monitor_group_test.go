//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorGroupBindingUpdateSemantics(t *testing.T) {
	groupID := int64(17)
	monitor := &ChannelMonitor{Provider: MonitorProviderOpenAI, Endpoint: "https://example.com", GroupID: &groupID, GroupName: "current name"}
	require.NoError(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{}))
	require.Equal(t, int64(17), *monitor.GroupID)

	nextID := int64(22)
	require.NoError(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{GroupID: &nextID}))
	require.Equal(t, nextID, *monitor.GroupID)
	*monitor.GroupID = 23
	require.Equal(t, int64(22), nextID, "the request pointer must not be retained")

	zero := int64(0)
	require.NoError(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{GroupID: &zero}))
	require.Nil(t, monitor.GroupID)
	require.Empty(t, monitor.GroupName, "unbinding must not turn a resolved name into a stale legacy label")

	legacy := "legacy label"
	require.NoError(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{GroupID: &zero, GroupName: &legacy}))
	require.Equal(t, legacy, monitor.GroupName)

	negative := int64(-1)
	require.ErrorIs(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{GroupID: &negative}), ErrChannelMonitorInvalidGroup)
}

func TestChannelMonitorCreateRejectsNonPositiveGroupBinding(t *testing.T) {
	for _, id := range []int64{0, -1} {
		require.ErrorIs(t, validateCreateParams(ChannelMonitorCreateParams{GroupID: &id}), ErrChannelMonitorInvalidGroup)
	}
}

func TestChannelMonitorUserViewKeepsGroupIdentity(t *testing.T) {
	id := int64(7)
	monitor := &ChannelMonitor{GroupID: &id, GroupName: "renamed group"}
	view := buildUserViewFromSummary(monitor, MonitorStatusSummary{}, nil, nil)
	require.Equal(t, monitor.GroupID, view.GroupID)
	require.Equal(t, monitor.GroupName, view.GroupName)
	*view.GroupID = 8
	require.Equal(t, int64(7), id)
}
