package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// A replaced attempt is a separate supplier expense. Never pass it to
// RecordUsage or merge its tokens into the successful downstream response.
func (s *OpenAIGatewayService) recordCollaborationFailedAttemptUsage(ctx context.Context, c *gin.Context, account *Account, body []byte, resp *http.Response, failure []byte) (string, error) {
	usage, known := collaborationFailedAttemptUsage(failure)
	if !known {
		return "usage_unknown", nil
	}
	if c == nil {
		return "record_failed", errors.New("collaboration attempt owner is unavailable")
	}
	key := getAPIKeyFromContext(c)
	if s == nil || account == nil || key == nil || key.ID <= 0 || key.UserID <= 0 || s.usageLogRepo == nil || s.usageBillingRepo == nil {
		return "record_failed", errors.New("collaboration attempt accounting dependencies are unavailable")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" || s.billingService == nil {
		return "cost_unknown", errors.New("collaboration attempt pricing is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if account.IsShadow() && s.accountRepo == nil {
		return "cost_unknown", errors.New("collaboration attempt credential account is unavailable")
	}
	billingAccount, err := resolveCredentialAccount(dbCtx, s.accountRepo, account)
	if err != nil {
		return "cost_unknown", err
	}
	pricingAt := OpenAIPricingAtFromContext(ctx)
	if pricingAt.IsZero() {
		pricingAt = time.Now()
	}
	tier := gjson.GetBytes(body, "service_tier").String()
	effort := gjson.GetBytes(body, "reasoning.effort").String()
	tierResult := &OpenAIForwardResult{Model: model, ServiceTier: optionalTrimmedStringPtr(tier),
		UpstreamResponseServiceTier: firstNonEmpty(gjson.GetBytes(failure, "response.service_tier").String(), gjson.GetBytes(failure, "service_tier").String())}
	ApplyOpenAIServiceTierBillingResolution(billingAccount, tierResult)
	tier = optionalStringValue(tierResult.ServiceTier)
	tokens := UsageTokens{
		InputTokens:  usage.InputTokens - usage.CacheReadInputTokens - usage.CacheCreationInputTokens,
		OutputTokens: usage.OutputTokens, CacheReadTokens: usage.CacheReadInputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens, ImageInputTokens: usage.ImageInputTokens,
		ImageOutputTokens: usage.ImageOutputTokens,
	}
	gate := openAILongContextBillingGate(billingAccount)
	exempt := openAISubscriptionLongContextExempt(billingAccount, tierResult)
	cost, err := s.calculateOpenAIRecordUsageTokenCost(dbCtx, key, model, 1, pricingAt, tokens, tier, effort, gate, exempt)
	if err != nil || cost == nil || !validSalesCost(cost.TotalCost) {
		return "cost_unknown", fmt.Errorf("price failed collaboration attempt: %w", errors.Join(err, errors.New("valid token cost is required")))
	}
	log := collaborationFailedAttemptLog(ctx, c, account, key, body, resp, failure, model, usage, cost)
	log.ServiceTier = tierResult.ServiceTier
	purchase, err := resolveAccountModelCostWithImages(dbCtx, s.billingService, account,
		CostInput{Model: model, Tokens: tokens, RequestCount: 1, PricingAt: pricingAt, ServiceTier: tier, ReasoningEffort: effort}, 0, nil)
	if err != nil {
		return "cost_unknown", fmt.Errorf("price failed collaboration purchase cost: %w", err)
	}
	if purchase != nil {
		one := 1.0
		log.AccountStatsCost, log.AccountRateMultiplier = purchase, &one
	} else if key.GroupID != nil {
		applyAccountStatsCost(dbCtx, log, s.channelService, s.billingService, account.ID, *key.GroupID,
			model, model, tokens, cost.TotalCost, pricingAt, accountStatsLongContextPricingEnabled(gate) && !exempt)
	}
	return s.persistCollaborationFailedAttemptUsage(dbCtx, account, body, log)
}

func collaborationFailedAttemptUsage(failure []byte) (OpenAIUsage, bool) {
	raw := gjson.GetBytes(failure, "response.usage")
	if !raw.Exists() {
		raw = gjson.GetBytes(failure, "usage")
	}
	// The shared extractor accepts an empty object. Here absence or a partial
	// counter set is explicitly unknown, never a zero-cost supplier attempt.
	for _, names := range [][2]string{{"input_tokens", "prompt_tokens"}, {"output_tokens", "completion_tokens"}} {
		counter := raw.Get(names[0])
		if !counter.Exists() {
			counter = raw.Get(names[1])
		}
		count, err := strconv.ParseInt(counter.Raw, 10, 64)
		if counter.Type != gjson.Number || err != nil || count < 0 {
			return OpenAIUsage{}, false
		}
	}
	usage, ok := openAIUsageFromGJSON(raw)
	if !ok || usage.CacheReadInputTokens > usage.InputTokens || usage.CacheCreationInputTokens > usage.InputTokens-usage.CacheReadInputTokens ||
		usage.ImageInputTokens > usage.InputTokens || usage.ImageOutputTokens > usage.OutputTokens {
		return OpenAIUsage{}, false
	}
	return usage, true
}

func collaborationFailedAttemptLog(ctx context.Context, c *gin.Context, account *Account, key *APIKey, body []byte, resp *http.Response, failure []byte, model string, usage OpenAIUsage, cost *CostBreakdown) *UsageLog {
	upstreamID := ""
	var configuredRequestID *string
	if resp != nil {
		upstreamID = strings.TrimSpace(resp.Header.Get("x-request-id"))
		configuredRequestID = usageUpstreamRequestIDPtr(account, resp.Header, false)
		if configuredRequestID != nil {
			upstreamID = *configuredRequestID
		}
	}
	identity := upstreamID
	if identity == "" {
		identity = extractOpenAIResponseIDFromJSONBytes(failure)
	}
	if identity == "" {
		identity, _ = ctx.Value(ctxkey.RequestID).(string)
	}
	if identity == "" {
		identity = generateRequestID()
	}
	multiplier := account.BillingRateMultiplier()
	log := &UsageLog{
		UserID: key.UserID, APIKeyID: key.ID, AccountID: account.ID, GroupID: key.GroupID,
		RequestID:         "collaboration_failed:" + collaborationHash(strconv.FormatInt(account.ID, 10)+":"+identity),
		UpstreamRequestID: configuredRequestID, Model: model, RequestedModel: model,
		UpstreamModel: optionalTrimmedStringPtr(model), SessionID: optionalTrimmedStringPtr(ExtractClientSessionID(c)),
		InputTokens:  usage.InputTokens - usage.CacheReadInputTokens - usage.CacheCreationInputTokens,
		OutputTokens: usage.OutputTokens, CacheReadTokens: usage.CacheReadInputTokens, CacheCreationTokens: usage.CacheCreationInputTokens,
		ImageInputTokens: usage.ImageInputTokens, ImageOutputTokens: usage.ImageOutputTokens,
		InputCost: cost.InputCost, OutputCost: cost.OutputCost, CacheReadCost: cost.CacheReadCost, CacheCreationCost: cost.CacheCreationCost,
		ImageInputCost: cost.ImageInputCost, ImageOutputCost: cost.ImageOutputCost, TotalCost: cost.TotalCost,
		ActualCost: 0, RateMultiplier: 0, AccountRateMultiplier: &multiplier, BillingType: BillingTypeBalance,
		BillingMode:     optionalTrimmedStringPtr(string(BillingModeToken)),
		ServiceTier:     optionalTrimmedStringPtr(gjson.GetBytes(body, "service_tier").String()),
		ReasoningEffort: optionalTrimmedStringPtr(gjson.GetBytes(body, "reasoning.effort").String()),
		Stream:          gjson.GetBytes(body, "stream").Bool(), CreatedAt: time.Now(),
		UpstreamEndpoint: optionalTrimmedStringPtr(openAIResponsesUpstreamEndpoint),
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		log.InboundEndpoint = optionalTrimmedStringPtr(c.Request.URL.Path)
	}
	return log
}

func (s *OpenAIGatewayService) persistCollaborationFailedAttemptUsage(ctx context.Context, account *Account, body []byte, log *UsageLog) (string, error) {
	cmd := &UsageBillingCommand{
		RequestID: log.RequestID, APIKeyID: log.APIKeyID, UserID: log.UserID, AccountID: account.ID,
		AccountType: account.Type, Model: log.Model, BillingType: log.BillingType,
		InputTokens: log.InputTokens, OutputTokens: log.OutputTokens,
		CacheCreationTokens: log.CacheCreationTokens, CacheReadTokens: log.CacheReadTokens,
		RequestPayloadHash: HashUsageRequestPayload(body),
		ServiceTier:        optionalStringValue(log.ServiceTier), ReasoningEffort: optionalStringValue(log.ReasoningEffort),
	}
	// Keep the existing account-quota convention; purchase-price statistics
	// remain in AccountStatsCost. All customer monetary fields stay zero.
	if account.SupportsAccountQuota() && account.HasAnyQuotaLimit() {
		cmd.AccountQuotaCost = log.TotalCost * account.BillingRateMultiplier()
		cmd.AccountQuotaHasDailyLimit = account.GetQuotaDailyLimit() > 0
		cmd.AccountQuotaHasWeeklyLimit = account.GetQuotaWeeklyLimit() > 0
	}
	if !validSalesCost(cmd.AccountQuotaCost) {
		return "cost_unknown", errors.New("failed collaboration account quota cost is invalid")
	}
	cmd.Normalize()
	_, quotaErr := s.usageBillingRepo.Apply(ctx, cmd)
	// Both writes use an independent attempt ID. Retrying a failed log write
	// can heal it without incrementing account quota a second time.
	_, logErr := s.usageLogRepo.Create(ctx, log)
	if err := errors.Join(quotaErr, logErr); err != nil {
		return "record_failed", err
	}
	if s.deferredService != nil {
		s.deferredService.ScheduleLastUsedUpdate(account.ID)
	}
	return "recorded", nil
}
