package conversationaudit

import (
	"context"
	"strings"
	"testing"
	"time"

	appconfig "github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCaptureServiceDisabledBeginIsAllocationFree(t *testing.T) {
	service := NewCaptureService(nil, nil)
	allocs := testing.AllocsPerRun(1000, func() {
		session := service.Begin(context.Background(), BeginInput{})
		session.SetRequestBody("openai", nil)
		session.Observe(ResponseEvent{})
		session.Finish(FinishResult{})
	})
	require.Zero(t, allocs)
	require.False(t, service.EffectiveEnabled())
	require.Equal(t, "disabled", service.Lifecycle())
}

func TestCaptureServiceReappliesEnableReceivedWhileDisabling(t *testing.T) {
	repository := NewRepository(nil)
	cfg := &appconfig.Config{ConversationAudit: appconfig.ConversationAuditConfig{
		ActiveKeyID: "v1",
		Keyring:     "v1=" + strings.Repeat("11", 32),
	}}
	manager := NewConfigManager(nil, nil, nil, repository, cfg)
	service := NewCaptureService(manager, repository)
	enabled := activeFromStored(defaultStoredConfig())
	enabled.Enabled = true
	service.ApplyConfig(enabled)

	session := service.Begin(context.Background(), BeginInput{
		AuditID: uuid.New(), CreatedAt: time.Now(), RequestID: "request-1",
		UserID: 1, APIKeyID: 2, Protocol: "openai", InboundEndpoint: "/v1/responses",
	})
	disabled := enabled
	disabled.Enabled = false
	disabled.ConfigVersion = 2
	service.ApplyConfig(disabled)
	require.Equal(t, "disabling", service.Lifecycle())

	reenabled := enabled
	reenabled.ConfigVersion = 3
	manager.snapshot.Store(&configSnapshot{stored: storedConfig{
		Enabled: true, RetentionDays: reenabled.RetentionDays,
		RequestMaxBytes: reenabled.RequestMaxBytes, ResponseMaxBytes: reenabled.ResponseMaxBytes,
		MemoryBudgetBytes: reenabled.MemoryBudgetBytes, WorkerCount: reenabled.WorkerCount,
		QueueCapacity: reenabled.QueueCapacity, ConfigVersion: reenabled.ConfigVersion,
	}, active: reenabled})
	service.ApplyConfig(reenabled)
	require.Equal(t, "disabling", service.Lifecycle())

	session.Finish(FinishResult{OutcomeStatus: OutcomeCompleted})
	require.Eventually(t, service.EffectiveEnabled, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, int64(3), service.activeConfig.Load().ConfigVersion)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, service.Shutdown(ctx))
}
