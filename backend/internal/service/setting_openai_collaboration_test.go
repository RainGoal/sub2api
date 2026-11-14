package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func enabledCollaborationTestSettings() OpenAICollaborationSettings {
	return OpenAICollaborationSettings{Enabled: true, PlaintextEnabled: true,
		LegacyCipherRecoveryEnabled: true, GroupIDs: []int64{7}, APIKeyIDs: []int64{11}}
}

func TestOpenAICollaborationScopeIsExplicit(t *testing.T) {
	settings := enabledCollaborationTestSettings()
	require.True(t, settings.Allows(7, 11))
	for _, scope := range [][2]int64{{0, 11}, {7, 0}, {8, 11}, {7, 12}} {
		require.False(t, settings.Allows(scope[0], scope[1]))
	}
	settings.APIKeyIDs = nil
	require.True(t, settings.Allows(7, 12))
	settings.GroupIDs = nil
	require.False(t, settings.Allows(7, 12))
	settings = enabledCollaborationTestSettings()
	settings.Enabled = false
	require.False(t, settings.Allows(7, 11))
}

func TestOpenAICollaborationSettingsFailClosed(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", "{bad", `{"enabled":true}`, `{"enabled":true,"group_ids":[-1]}`} {
		t.Run(raw, func(t *testing.T) {
			repo := &panelRateLimitSettingRepo{values: map[string]string{SettingKeyOpenAICollaborationSettings: raw}}
			svc := NewSettingService(repo, &config.Config{})
			require.False(t, svc.GetOpenAICollaborationSettingsCached(context.Background()).Enabled)
		})
	}
	svc := NewSettingService(&panelRateLimitSettingRepo{getValueErr: errors.New("database offline")}, &config.Config{})
	require.False(t, svc.GetOpenAICollaborationSettingsCached(context.Background()).Enabled)
	var absent *SettingService
	require.False(t, absent.GetOpenAICollaborationSettingsCached(context.Background()).Enabled)
}

func TestOpenAICollaborationSettingsRejectInvalidSelectors(t *testing.T) {
	for _, settings := range []OpenAICollaborationSettings{
		{Enabled: true}, {GroupIDs: []int64{-1}}, {GroupIDs: []int64{7, 7}},
		{APIKeyIDs: []int64{0}}, {APIKeyIDs: []int64{11, 11}},
	} {
		require.Error(t, ValidateOpenAICollaborationSettings(settings))
	}
}

func TestOpenAICollaborationSettingsShutdownAndCacheOwnership(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	first := NewSettingService(repo, &config.Config{})
	second := NewSettingService(repo, &config.Config{})
	settings := enabledCollaborationTestSettings()
	require.NoError(t, first.SetOpenAICollaborationSettings(ctx, settings))
	settings.GroupIDs[0] = 999
	got := first.GetOpenAICollaborationSettingsCached(ctx)
	require.True(t, got.Allows(7, 11))
	got.APIKeyIDs[0] = 999
	require.True(t, first.GetOpenAICollaborationSettingsCached(ctx).Allows(7, 11))
	require.True(t, second.GetOpenAICollaborationSettingsCached(ctx).Enabled)
	require.NoError(t, first.SetOpenAICollaborationSettings(ctx, DefaultOpenAICollaborationSettings()))
	require.False(t, first.GetOpenAICollaborationSettingsCached(ctx).Enabled)
	second.openAICollaborationCache.Store(&cachedOpenAICollaborationSettings{
		settings: enabledCollaborationTestSettings(), expiresAt: time.Now().Add(-time.Second),
	})
	require.False(t, second.GetOpenAICollaborationSettingsCached(ctx).Enabled)
}

func TestOpenAICollaborationExpiredEnabledCacheFailsClosedOnReadError(t *testing.T) {
	repo := &panelRateLimitSettingRepo{getValueErr: errors.New("database offline")}
	svc := NewSettingService(repo, &config.Config{})
	svc.openAICollaborationCache.Store(&cachedOpenAICollaborationSettings{
		settings: enabledCollaborationTestSettings(), expiresAt: time.Now().Add(-time.Second),
	})
	require.False(t, svc.GetOpenAICollaborationSettingsCached(context.Background()).Enabled)
}

type blockingCollaborationSettingsRepo struct {
	*panelRateLimitSettingRepo
	readStarted chan struct{}
	releaseRead chan struct{}
}

func (r *blockingCollaborationSettingsRepo) GetValue(ctx context.Context, key string) (string, error) {
	raw, err := r.panelRateLimitSettingRepo.GetValue(ctx, key)
	close(r.readStarted)
	select {
	case <-r.releaseRead:
		return raw, err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestOpenAICollaborationShutdownCannotBeOverwrittenByInflightRefresh(t *testing.T) {
	ctx := context.Background()
	repo := &blockingCollaborationSettingsRepo{panelRateLimitSettingRepo: &panelRateLimitSettingRepo{
		values: map[string]string{SettingKeyOpenAICollaborationSettings: `{"enabled":true,"group_ids":[7]}`},
	}, readStarted: make(chan struct{}), releaseRead: make(chan struct{})}
	svc := NewSettingService(repo, &config.Config{})
	readDone := make(chan struct{})
	go func() { svc.GetOpenAICollaborationSettingsCached(ctx); close(readDone) }()
	<-repo.readStarted
	writeDone := make(chan error, 1)
	go func() { writeDone <- svc.SetOpenAICollaborationSettings(ctx, DefaultOpenAICollaborationSettings()) }()
	select {
	case err := <-writeDone:
		close(repo.releaseRead)
		<-readDone
		require.NoError(t, err)
	case <-time.After(50 * time.Millisecond):
		close(repo.releaseRead)
		<-readDone
		require.NoError(t, <-writeDone)
	}
	require.False(t, svc.GetOpenAICollaborationSettingsCached(ctx).Enabled)
}
