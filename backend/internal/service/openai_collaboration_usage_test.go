//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const collaborationFailedUsageBody = `{"model":"gpt-5.1","stream":true,"input":[]}`
const collaborationFailedUsageResponse = `{"type":"response.failed","response":{"id":"resp_failure","usage":{"input_tokens":1000,"output_tokens":20,"input_tokens_details":{"cached_tokens":200}}}}`

func collaborationUsageContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	groupID := int64(31)
	c.Set("api_key", &APIKey{ID: 21, UserID: 11, User: &User{ID: 11}, GroupID: &groupID,
		Group: &Group{ID: groupID, Platform: PlatformOpenAI}, Quota: 100})
	return c
}

func TestCollaborationFailedAttemptUsageRecordsSupplierWithoutCustomerCharge(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	billing := &openAIRecordUsageBillingRepoStub{}
	users := &openAIRecordUsageUserRepoStub{}
	subscriptions := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, users, subscriptions, nil)
	account := modelCostAccount(PlatformOpenAI, ChannelModelPricing{
		Models: []string{"gpt-5.1"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0.001),
		OutputPrice: testPtrFloat64(0.002), CacheReadPrice: testPtrFloat64(0.0005),
	})
	account.Extra["quota_limit"] = 100.0
	resp := &http.Response{Header: http.Header{"X-Request-Id": {"supplier-failure-1"}}}
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "same-client-turn")
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	state, err := svc.recordCollaborationFailedAttemptUsage(ctx, collaborationUsageContext(), account,
		[]byte(collaborationFailedUsageBody), resp, []byte(collaborationFailedUsageResponse))
	require.NoError(t, err)
	require.Equal(t, "recorded", state)
	require.NoError(t, logs.lastCtxErr, "supplier consumption survives client cancellation")
	require.NoError(t, billing.lastCtxErr)
	log, cmd := logs.lastLog, billing.lastCmd
	require.Equal(t, 800, log.InputTokens)
	require.Equal(t, 200, log.CacheReadTokens)
	require.Equal(t, 20, log.OutputTokens)
	require.Greater(t, log.TotalCost, 0.0)
	require.InDelta(t, 0.94, *log.AccountStatsCost, 1e-12)
	require.Equal(t, 1.0, *log.AccountRateMultiplier)
	require.Zero(t, log.ActualCost)
	require.InDelta(t, log.TotalCost*account.BillingRateMultiplier(), cmd.AccountQuotaCost, 1e-8)
	require.Zero(t, cmd.BalanceCost)
	require.Zero(t, cmd.BalanceHoldAmount)
	require.Zero(t, cmd.SubscriptionCost)
	require.Nil(t, cmd.SubscriptionID)
	require.Zero(t, cmd.APIKeyQuotaCost)
	require.Zero(t, cmd.APIKeyRateLimitCost)
	require.Zero(t, users.deductCalls)
	require.Equal(t, log.RequestID, cmd.RequestID)
	require.Contains(t, log.RequestID, "collaboration_failed:")
	require.NotEqual(t, resolveUsageBillingRequestID(ctx, "successful-attempt"), cmd.RequestID)
}

func TestCollaborationFailedAttemptUsageUnknownNeverBecomesFreeRow(t *testing.T) {
	for _, usage := range []string{"", `null`, `{}`, `{"input_tokens":12}`, `{"output_tokens":0}`,
		`{"input_tokens":-1,"output_tokens":0}`, `{"input_tokens":"12","output_tokens":0}`,
		`{"input_tokens":1.5,"output_tokens":0}`, `{"input_tokens":1,"output_tokens":0,"input_tokens_details":{"cached_tokens":2}}`} {
		t.Run(usage, func(t *testing.T) {
			logs := &openAIRecordUsageLogRepoStub{}
			billing := &openAIRecordUsageBillingRepoStub{}
			svc := &OpenAIGatewayService{usageLogRepo: logs, usageBillingRepo: billing}
			failure := `{"response":{"status":"failed"}}`
			if usage != "" {
				failure = `{"response":{"usage":` + usage + `}}`
			}
			state, err := svc.recordCollaborationFailedAttemptUsage(context.Background(), nil, nil, nil, nil, []byte(failure))
			require.NoError(t, err)
			require.Equal(t, "usage_unknown", state)
			require.Zero(t, logs.calls)
			require.Zero(t, billing.calls)
		})
	}
}

func TestCollaborationFailedAttemptExplicitZeroUsageIsKnown(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	billing := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	state, err := svc.recordCollaborationFailedAttemptUsage(context.Background(), collaborationUsageContext(), account,
		[]byte(collaborationFailedUsageBody), nil, []byte(`{"usage":{"input_tokens":0,"output_tokens":0}}`))
	require.NoError(t, err)
	require.Equal(t, "recorded", state)
	require.Zero(t, logs.lastLog.TotalCost)
	require.Zero(t, billing.lastCmd.AccountQuotaCost)
}

func TestCollaborationFailedAttemptCostOrRepositoryUnavailableIsExplicit(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{}
	billing := &openAIRecordUsageBillingRepoStub{}
	svc := &OpenAIGatewayService{usageLogRepo: logs, usageBillingRepo: billing}
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	state, err := svc.recordCollaborationFailedAttemptUsage(context.Background(), collaborationUsageContext(), account,
		[]byte(collaborationFailedUsageBody), nil, []byte(collaborationFailedUsageResponse))
	require.Error(t, err)
	require.Equal(t, "cost_unknown", state)
	require.Zero(t, logs.calls)
	require.Zero(t, billing.calls)
	svc.usageBillingRepo = nil
	state, err = svc.recordCollaborationFailedAttemptUsage(context.Background(), collaborationUsageContext(), account,
		[]byte(collaborationFailedUsageBody), nil, []byte(collaborationFailedUsageResponse))
	require.Error(t, err)
	require.Equal(t, "record_failed", state)
	require.Zero(t, logs.calls)
}

type collaborationAttemptBillingRepo struct {
	UsageBillingRepository
	keys        map[string]string
	accountCost float64
}

func (r *collaborationAttemptBillingRepo) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	if old, exists := r.keys[cmd.RequestID]; exists {
		if old != cmd.RequestFingerprint {
			return nil, ErrUsageBillingRequestConflict
		}
		return &UsageBillingApplyResult{Applied: false}, nil
	}
	r.keys[cmd.RequestID] = cmd.RequestFingerprint
	r.accountCost += cmd.AccountQuotaCost
	return &UsageBillingApplyResult{Applied: true}, nil
}

func TestCollaborationFailedAttemptLogRetryDoesNotRepeatAccountQuota(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{err: errors.New("temporary log failure")}
	billing := &collaborationAttemptBillingRepo{keys: make(map[string]string)}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"quota_limit": 100.0}}
	resp := &http.Response{Header: http.Header{"X-Request-Id": {"failure-1"}}}
	c := collaborationUsageContext()
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "reused-client-id")
	state, err := svc.recordCollaborationFailedAttemptUsage(ctx, c, account, []byte(collaborationFailedUsageBody), resp, []byte(collaborationFailedUsageResponse))
	require.ErrorContains(t, err, "temporary log failure")
	require.Equal(t, "record_failed", state)
	charged, requestID := billing.accountCost, logs.lastLog.RequestID
	require.Greater(t, charged, 0.0)
	logs.err = nil
	state, err = svc.recordCollaborationFailedAttemptUsage(ctx, c, account, []byte(collaborationFailedUsageBody), resp, []byte(collaborationFailedUsageResponse))
	require.NoError(t, err)
	require.Equal(t, "recorded", state)
	require.Equal(t, requestID, logs.lastLog.RequestID)
	require.Equal(t, charged, billing.accountCost)
	resp.Header.Set("X-Request-Id", "failure-2")
	state, err = svc.recordCollaborationFailedAttemptUsage(ctx, c, account, []byte(collaborationFailedUsageBody), resp, []byte(collaborationFailedUsageResponse))
	require.NoError(t, err)
	require.Equal(t, "recorded", state)
	require.NotEqual(t, requestID, logs.lastLog.RequestID, "distinct supplier attempts must not collapse under a client request ID")
	require.InDelta(t, charged*2, billing.accountCost, 1e-12)
}

func TestCollaborationFailedAttemptQuotaFailureStillRecordsSupplierCost(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	billing := &openAIRecordUsageBillingRepoStub{err: errors.New("temporary quota failure")}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
	state, err := svc.recordCollaborationFailedAttemptUsage(context.Background(), collaborationUsageContext(),
		&Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		[]byte(collaborationFailedUsageBody), nil, []byte(collaborationFailedUsageResponse))
	require.ErrorContains(t, err, "temporary quota failure")
	require.Equal(t, "record_failed", state)
	require.Equal(t, 1, logs.calls)
	require.Greater(t, logs.lastLog.TotalCost, 0.0)
	require.Zero(t, logs.lastLog.ActualCost)
}

func TestCollaborationFailedAttemptUsesExistingServiceTierResolution(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, &openAIRecordUsageBillingRepoStub{}, nil, nil, nil)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	state, err := svc.recordCollaborationFailedAttemptUsage(context.Background(), collaborationUsageContext(), account,
		[]byte(`{"model":"gpt-5.1","service_tier":"priority"}`), nil,
		[]byte(`{"response":{"service_tier":"default","usage":{"input_tokens":1000,"output_tokens":20}}}`))
	require.NoError(t, err)
	require.Equal(t, "recorded", state)
	require.Equal(t, "default", *logs.lastLog.ServiceTier)
	standard := expectedOpenAICost(t, svc, "gpt-5.1", OpenAIUsage{InputTokens: 1000, OutputTokens: 20}, 1)
	require.InDelta(t, standard.TotalCost, logs.lastLog.TotalCost, 1e-12)
}

func TestCollaborationFailedAttemptMissingShadowParentLeavesCostUnknown(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, &openAIRecordUsageBillingRepoStub{}, nil, nil, nil)
	parentID := int64(8)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID}
	state, err := svc.recordCollaborationFailedAttemptUsage(context.Background(), collaborationUsageContext(), account,
		[]byte(collaborationFailedUsageBody), nil, []byte(collaborationFailedUsageResponse))
	require.Error(t, err)
	require.Equal(t, "cost_unknown", state)
	require.Zero(t, logs.calls)
}
