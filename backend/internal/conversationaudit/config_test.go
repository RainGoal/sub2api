package conversationaudit

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationAuditConfigDefaultsOff(t *testing.T) {
	config, err := parseStoredConfig("")
	require.NoError(t, err)
	require.False(t, config.Enabled)
	require.Equal(t, DefaultRetentionDays, config.RetentionDays)
	require.Equal(t, DefaultRequestMaxBytes, config.RequestMaxBytes)
	require.Equal(t, DefaultResponseMaxBytes, config.ResponseMaxBytes)
	require.Equal(t, int64(DefaultMemoryBudgetBytes), config.MemoryBudgetBytes)
	require.Equal(t, DefaultWorkerCount, config.WorkerCount)
	require.Equal(t, DefaultQueueCapacity, config.QueueCapacity)
	require.Equal(t, int64(1), config.ConfigVersion)
}

func TestConversationAuditConfigValidationBounds(t *testing.T) {
	valid := defaultStoredConfig()
	require.NoError(t, validateStoredConfig(valid))
	tests := []func(*storedConfig){
		func(config *storedConfig) { config.RetentionDays = 0 },
		func(config *storedConfig) { config.RetentionDays = 366 },
		func(config *storedConfig) { config.RequestMaxBytes = MinPayloadMaxBytes - 1 },
		func(config *storedConfig) { config.ResponseMaxBytes = MaxPayloadMaxBytes + 1 },
		func(config *storedConfig) { config.MemoryBudgetBytes = MinMemoryBudgetBytes - 1 },
		func(config *storedConfig) { config.WorkerCount = MaxWorkerCount + 1 },
		func(config *storedConfig) { config.QueueCapacity = MaxQueueCapacity + 1 },
	}
	for _, mutate := range tests {
		config := valid
		mutate(&config)
		require.ErrorIs(t, validateStoredConfig(config), ErrInvalidConfig)
	}
}

func TestConversationAuditStoredConfigRejectsMalformedJSON(t *testing.T) {
	_, err := parseStoredConfig("{")
	require.True(t, errors.Is(err, ErrInvalidConfig))
}
