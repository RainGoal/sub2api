//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestVideoTaskBulkCredentialValidationMergesCurrentAccount(t *testing.T) {
	capability, found := videoprovider.LookupVideoModelCapability("omni-fast", nil)
	require.True(t, found)
	account := &Account{Platform: PlatformSeedance, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "original", "base_url": "https://supplier.example/v1",
			"video_provider": "video_tasks_json_v1", "model_mapping": map[string]any{"omni-fast": "omni-fast"}},
		Extra: map[string]any{VideoModelCapabilitiesExtra: []videoprovider.VideoModelCapability{capability}}}
	require.NoError(t, validateVideoTaskBulkUpdate(account, map[string]any{"api_key": "replacement"}, nil))
	require.Equal(t, "original", account.Credentials["api_key"])
	for _, credentials := range []map[string]any{
		{"base_url": "http://supplier.example/v1"},
		{"video_provider": "unsupported-protocol"},
		{"model_mapping": map[string]any{"omni-fast": "different-model"}},
	} {
		require.Error(t, validateVideoTaskBulkUpdate(account, credentials, nil))
	}
	require.Error(t, validateVideoTaskBulkUpdate(account, nil, map[string]any{VideoSupplierNameExtra: 42}))
	require.Error(t, validateVideoTaskBulkUpdate(account, nil, map[string]any{
		VideoModelCapabilitiesExtra: []any{map[string]any{"model": "omni-fast", "typo": true}},
	}))
}
