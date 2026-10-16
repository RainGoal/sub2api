//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClientFailedSSEPreservesCreatedAtAfterPrivacyRewrite(t *testing.T) {
	frame := buildOpenAIClientResponseFailedSSE("resp_merge", "public-model",
		[]byte(`{"type":"error","sequence_number":7,"error":{"code":"safety_error","message":"private upstream detail"}}`), "", 1700000000)
	payloads := sseDataPayloads(frame)
	require.Len(t, payloads, 1)
	require.EqualValues(t, 1700000000, gjson.Get(payloads[0], "response.created_at").Int())
	require.EqualValues(t, 7, gjson.Get(payloads[0], "sequence_number").Int())
	require.Equal(t, "public-model", gjson.Get(payloads[0], "response.model").String())
	require.NotContains(t, frame, "private upstream detail")
}

func TestGeminiCacheStoragePreservesAccountPurchasePricing(t *testing.T) {
	for _, tokens := range []int{10000, 0} {
		t.Run(strconv.Itoa(tokens), func(t *testing.T) {
			svc, usageRepo := newGeminiCacheBillingGatewayForTest(t)
			account := modelCostAccount(PlatformGemini, ChannelModelPricing{
				Models: []string{"gemini-3.8-flash"}, BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(0.05),
			})
			groupID := int64(910)
			err := svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: &ForwardResult{RequestID: "merge-cache-pricing", Model: "gemini-3.8-flash",
					Usage: ClaudeUsage{InputTokens: tokens}, CacheStorageTokenHours: 20000},
				APIKey: &APIKey{ID: 911, GroupID: &groupID,
					Group: &Group{ID: groupID, Platform: PlatformGemini, RateMultiplier: 1, Hydrated: true}},
				User: &User{ID: 912}, Account: account,
			})
			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			if tokens == 0 {
				require.Nil(t, usageRepo.lastLog.AccountStatsCost, "extending cache must not incur a model request purchase charge")
				require.InDelta(t, 0.01, usageRepo.lastLog.TotalCost, 1e-12)
				require.InDelta(t, 0.0025, usageRepo.lastLog.TotalCost**usageRepo.lastLog.AccountRateMultiplier, 1e-12)
			} else {
				require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
				require.InDelta(t, 0.0525, *usageRepo.lastLog.AccountStatsCost, 1e-12, "purchase cost includes cache storage once at the original account rate")
				require.Equal(t, 1.0, *usageRepo.lastLog.AccountRateMultiplier)
			}
		})
	}
}
