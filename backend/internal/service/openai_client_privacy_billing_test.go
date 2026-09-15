//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIClientPrivacyBillingSourcesUnchanged(t *testing.T) {
	usage := OpenAIUsage{InputTokens: 2200, OutputTokens: 300, CacheReadInputTokens: 500}
	cheaper, pricier, _, _ := orderedResponseBillingModels(t, NewBillingService(&config.Config{}, nil),
		UsageTokens{InputTokens: 1700, OutputTokens: 300, CacheReadTokens: 500}, openAICheapFixtureModel, openAIPriceyFixtureModel)
	for _, scenario := range []struct{ source, public, sent, declared, billed string }{
		{BillingModelSourceRequested, cheaper, pricier, pricier, cheaper},
		{BillingModelSourceUpstream, cheaper, pricier, cheaper, pricier},
		{BillingModelSourceChannelMapped, cheaper, pricier, cheaper, pricier},
		{BillingModelSourceResponse, pricier, pricier, cheaper, cheaper},
	} {
		for _, subscription := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/subscription=%v/stream=%v", scenario.source, subscription, stream), func(t *testing.T) {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					request := fmt.Sprintf(`{"model":%q,"input":"hello","stream":%v}`, scenario.public, stream)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request))
					response := fmt.Sprintf(`{"id":"resp_billing","model":%q,"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":2200,"output_tokens":300,"input_tokens_details":{"cached_tokens":500}}}`, scenario.declared)
					contentType := "application/json"
					if stream {
						contentType = "text/event-stream"
						response = "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200,
						Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {"rid_billing"}}, Body: io.NopCloser(strings.NewReader(response)),
					}}
					account := &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
						"api_key": "test-token", "model_mapping": map[string]any{scenario.public: scenario.sent},
					}}
					svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
					forwarded, err := svc.Forward(context.Background(), c, account, []byte(request))
					require.NoError(t, err)
					require.NotNil(t, forwarded)
					require.Equal(t, scenario.public, forwarded.Model)
					require.Equal(t, scenario.sent, forwarded.BillingModel)
					require.Equal(t, scenario.sent, forwarded.UpstreamModel)
					require.Equal(t, scenario.declared, forwarded.UpstreamResponseModel)
					require.Equal(t, usage, forwarded.Usage)
					clientPayload := rec.Body.Bytes()
					if stream {
						_, event, ok := extractOpenAISSETerminalEvent(rec.Body.String())
						require.True(t, ok)
						clientPayload = []byte(gjson.GetBytes(event, "response").Raw)
					}
					require.Equal(t, scenario.public, gjson.GetBytes(clientPayload, "model").String())
					verifyOpenAIClientPrivacyBilling(t, forwarded, account, scenario.source, scenario.public, scenario.sent, scenario.declared, scenario.billed, subscription)
				})
			}
		}
	}
}

func verifyOpenAIClientPrivacyBilling(t *testing.T, forwarded *OpenAIForwardResult, account *Account, source, public, sent, declared, billed string, subscription bool) {
	t.Helper()
	record := func(result *OpenAIForwardResult) (*UsageLog, *UsageBillingCommand) {
		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		billingRepo := &openAIRecordUsageBillingRepoStub{}
		svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
		group := &Group{ID: 40, RateMultiplier: 1.1}
		var sub *UserSubscription
		if subscription {
			group.SubscriptionType = SubscriptionTypeSubscription
			sub = &UserSubscription{ID: 50}
		}
		err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: result, Account: account, User: &User{ID: 20}, Subscription: sub,
			APIKey:             &APIKey{ID: 10, GroupID: &group.ID, Group: group, Quota: 100},
			ChannelUsageFields: ChannelUsageFields{ChannelID: 9, OriginalModel: public, ChannelMappedModel: sent, BillingModelSource: source},
		})
		require.NoError(t, err)
		require.Equal(t, 1, billingRepo.calls)
		require.NotNil(t, usageRepo.lastLog)
		require.NotNil(t, billingRepo.lastCmd)
		expected := expectedOpenAICost(t, svc, billed, result.Usage, 1.1)
		require.Positive(t, expected.ActualCost)
		require.InDelta(t, expected.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
		return usageRepo.lastLog, billingRepo.lastCmd
	}
	// This is the pre-presentation billing input. Comparing the real Forward
	// result against it catches any accidental A-for-B/C substitution.
	baseline := &OpenAIForwardResult{RequestID: "rid_billing", ResponseID: "resp_billing",
		Model: public, BillingModel: sent, UpstreamModel: sent, UpstreamResponseModel: declared,
		Usage: OpenAIUsage{InputTokens: 2200, OutputTokens: 300, CacheReadInputTokens: 500}, Duration: time.Second,
		Stream: forwarded.Stream,
	}
	wantLog, wantCommand := record(baseline)
	gotLog, gotCommand := record(forwarded)
	require.Equal(t, wantLog.TotalCost, gotLog.TotalCost)
	require.Equal(t, wantLog.ActualCost, gotLog.ActualCost)
	require.Equal(t, wantLog.AccountStatsCost, gotLog.AccountStatsCost)
	require.Equal(t, wantCommand.BalanceCost, gotCommand.BalanceCost)
	require.Equal(t, wantCommand.SubscriptionCost, gotCommand.SubscriptionCost)
	require.Equal(t, wantCommand.AccountQuotaCost, gotCommand.AccountQuotaCost)
	require.Equal(t, wantCommand.APIKeyQuotaCost, gotCommand.APIKeyQuotaCost)
	require.Equal(t, wantCommand.APIKeyRateLimitCost, gotCommand.APIKeyRateLimitCost)
	require.Equal(t, public, gotLog.RequestedModel)
	require.NotNil(t, gotLog.UpstreamResponseModel)
	require.Equal(t, declared, *gotLog.UpstreamResponseModel)
	if subscription {
		require.Positive(t, gotCommand.SubscriptionCost)
		require.Zero(t, gotCommand.BalanceCost)
	} else {
		require.Positive(t, gotCommand.BalanceCost)
		require.Zero(t, gotCommand.SubscriptionCost)
	}
}
