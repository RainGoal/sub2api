package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSeedanceAccountCredentials(t *testing.T) {
	require.NoError(t, validateSeedanceAccountCredentials(PlatformSeedance, AccountTypeAPIKey, map[string]any{
		"api_key":  "sk-test",
		"base_url": "https://api.bblabu.ai/v1",
	}))
	require.Error(t, validateSeedanceAccountCredentials(PlatformSeedance, AccountTypeOAuth, map[string]any{"api_key": "sk-test"}))
	require.Error(t, validateSeedanceAccountCredentials(PlatformSeedance, AccountTypeAPIKey, map[string]any{}))
	require.Error(t, validateSeedanceAccountCredentials(PlatformSeedance, AccountTypeAPIKey, map[string]any{
		"api_key": "sk-test", "base_url": "http://public.example/v1",
	}))
}

func TestSeedanceSchedulerSelectsSeedanceAccount(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(91)
	account := Account{
		ID: 901, Platform: PlatformSeedance, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "seedance-key"},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:       &schedulerTestGatewayCache{}, cfg: cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(), &groupID, "", "", "Seedance-2.0", nil,
		OpenAIUpstreamTransportHTTPSSE, "", false, false, false, PlatformSeedance,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, account.ID, selection.Account.ID)
}

func TestAccountSeedanceAccessors(t *testing.T) {
	account := &Account{Platform: PlatformSeedance, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": " key "}}
	require.True(t, account.IsSeedance())
	require.False(t, account.IsOpenAICompatible())
	require.True(t, account.IsOpenAISchedulerCompatible())
	require.Equal(t, "key", account.GetSeedanceAPIKey())
	require.Equal(t, "https://api.bblabu.ai/v1", account.GetSeedanceBaseURL())
	require.Equal(t, PlatformSeedance, NormalizeOpenAICompatiblePlatform(PlatformSeedance))
	require.Contains(t, schedulerSnapshotPlatforms(), PlatformSeedance)
	require.Contains(t, AllowedQuotaPlatforms, PlatformSeedance)

	account.Credentials["video_provider"] = "fflink_v1"
	delete(account.Credentials, "base_url")
	require.Equal(t, "fflink_v1", string(account.GetVideoProviderID()))
	require.Equal(t, "https://api.fflink.top/v1", account.GetSeedanceBaseURL())
}
