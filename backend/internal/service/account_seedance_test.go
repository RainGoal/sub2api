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

func TestSeedanceModelSelectionValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		mapping  any
		valid    bool
	}{
		{"legacy unrestricted", "bblabu_v1", nil, true},
		{"empty unrestricted", "fflink_v1", map[string]any{}, true},
		{"fflink fast", "fflink_v1", map[string]any{"seedance-2.0-fast": "seedance-2.0-fast"}, true},
		{"bblabu alias", "bblabu_v1", map[string]any{"bytedance/seedance-2.5": "Seedance-2.5"}, true},
		{"wrong provider", "bblabu_v1", map[string]any{"seedance-2.0-fast": "seedance-2.0-fast"}, false},
		{"unknown model", "fflink_v1", map[string]any{"kling-3.0": "kling-3.0"}, false},
		{"model rewrite", "fflink_v1", map[string]any{"seedance-2.0": "seedance-2.5"}, false},
		{"wildcard", "fflink_v1", map[string]any{"seedance-*": "seedance-*"}, false},
		{"non string target", "fflink_v1", map[string]any{"seedance-2.0": true}, false},
		{"non object", "fflink_v1", []string{"seedance-2.0"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSeedanceAccountCredentials(PlatformSeedance, AccountTypeAPIKey, map[string]any{
				"api_key": "test-key", "video_provider": tc.provider, "model_mapping": tc.mapping,
			})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSeedanceModelSelectionMatchesCanonicalAliases(t *testing.T) {
	for _, selected := range []string{"seedance-2.5", "Seedance-2.5", "bytedance/seedance-2.5"} {
		account := &Account{Platform: PlatformSeedance, Credentials: map[string]any{
			"video_provider": "fflink_v1", "model_mapping": map[string]any{selected: selected},
		}}
		for _, request := range []string{"seedance-2.5", "Seedance-2.5", "bytedance/seedance-2.5"} {
			require.True(t, account.IsModelSupported(request), "selected=%s requested=%s", selected, request)
		}
		require.False(t, account.IsModelSupported("seedance-2.0"))
		require.False(t, account.IsModelSupported("seedance-2.0-fast"))
		require.False(t, account.IsModelSupported("kling-3.0"))
	}
	account := &Account{Platform: PlatformSeedance, Credentials: map[string]any{
		"video_provider": "bblabu_v1", "model_mapping": map[string]any{"seedance-2.0-fast": "seedance-2.0-fast"},
	}}
	require.False(t, account.IsModelSupported("seedance-2.0-fast"))
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
