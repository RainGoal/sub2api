package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) SeedanceVideoGeneration(c *gin.Context) {
	h.handleSeedanceVideo(c, service.SeedanceVideoEndpointCreate, "")
}

func (h *OpenAIGatewayHandler) SeedanceVideoStatus(c *gin.Context) {
	h.handleSeedanceVideo(c, service.SeedanceVideoEndpointStatus, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) SeedanceVideoContent(c *gin.Context) {
	h.handleSeedanceVideo(c, service.SeedanceVideoEndpointContent, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) SeedanceVideoCancel(c *gin.Context) {
	h.handleSeedanceVideo(c, service.SeedanceVideoEndpointCancel, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) handleSeedanceVideo(c *gin.Context, endpoint service.SeedanceVideoEndpoint, taskID string) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(c, "handler.openai_gateway.seedance_video",
		zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID), zap.String("endpoint", string(endpoint)))
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	var body []byte
	var createRequest *service.SeedanceVideoCreateRequest
	var requestInfo service.SeedanceVideoRequestInfo
	var err error
	if endpoint == service.SeedanceVideoEndpointCreate {
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			if maxErr, ok := extractMaxBytesError(err); ok {
				h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
				return
			}
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			return
		}
		createRequest, body, requestInfo, err = service.PrepareSeedanceVideoRequest(body)
		if err != nil {
			h.errorResponse(c, http.StatusUnprocessableEntity, "invalid_request_error", err.Error())
			return
		}
		if apiKey.Group == nil || service.LookupVideoModelPrice(apiKey.Group.VideoModelPrices, requestInfo.Model, requestInfo.Resolution) == nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "video_pricing_not_configured",
				"Seedance video pricing is not configured for this model and resolution")
			return
		}
		taskID = ""
	} else if strings.TrimSpace(taskID) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "request_id is required")
		return
	}

	requestModel := requestInfo.Model
	setOpsRequestContext(c, requestModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}
	if endpoint == service.SeedanceVideoEndpointCreate {
		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.PlatformSeedance); err != nil {
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.errorResponse(c, status, code, message)
			return
		}
	}

	var pendingTemplate service.SeedanceVideoPendingBilling
	taskNeedsRelease := false
	holdReserved := false
	if endpoint == service.SeedanceVideoEndpointCreate {
		pricing, pricingErr := h.gatewayService.ResolveSeedanceVideoPricingSnapshot(c.Request.Context(), apiKey, requestInfo)
		if pricingErr != nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "video_pricing_not_configured", pricingErr.Error())
			return
		}
		isSubscriptionBilling := subscription != nil && apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
		pendingTemplate = service.SeedanceVideoPendingBilling{
			StateID: service.NewSeedanceVideoStateID(), HoldID: service.NewSeedanceVideoHoldID(),
			UserID: subject.UserID, APIKeyID: apiKey.ID, GroupID: apiKey.GroupID,
			Model: requestInfo.Model, Resolution: requestInfo.Resolution,
			DurationSeconds: requestInfo.DurationSeconds, ReferenceVideoCount: requestInfo.ReferenceVideoCount,
			OriginalModel:      requestInfo.RequestedModel,
			CreatedAt:          time.Now().UTC().Format(time.RFC3339Nano),
			RequestPayloadHash: service.HashUsageRequestPayload(body),
			TotalCostPerSecond: pricing.TotalCostPerSecond, ActualCostPerSecond: pricing.ActualCostPerSecond,
			RateMultiplier: pricing.RateMultiplier, IsSubscriptionBilling: isSubscriptionBilling,
		}
		if isSubscriptionBilling {
			pendingTemplate.SubscriptionID = &subscription.ID
		} else {
			pendingTemplate.HoldAmount = pricing.HoldAmount
		}
		if beginErr := h.gatewayService.BeginSeedanceVideoTask(c.Request.Context(), &pendingTemplate); beginErr != nil {
			reqLog.Error("seedance_video.begin_task_failed", zap.Error(beginErr))
			h.errorResponse(c, http.StatusServiceUnavailable, "seedance_state_persistence_failed",
				"Failed to persist Seedance video task state")
			return
		}
		taskNeedsRelease = true
		defer func() {
			if !taskNeedsRelease {
				return
			}
			releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if holdReserved {
				if releaseErr := h.gatewayService.ReleaseSeedanceVideoBalance(releaseCtx, &pendingTemplate); releaseErr != nil {
					reqLog.Error("seedance_video.release_uncommitted_hold_failed", zap.Error(releaseErr))
					return
				}
			}
			if releaseErr := h.gatewayService.ReleaseSeedanceVideoTask(releaseCtx, &pendingTemplate); releaseErr != nil {
				reqLog.Error("seedance_video.release_uncommitted_task_failed", zap.Error(releaseErr))
			}
		}()
		if pendingTemplate.HoldAmount > 0 {
			if reserveErr := h.gatewayService.ReserveSeedanceVideoBalance(
				c.Request.Context(), subject.UserID, apiKey.ID, pendingTemplate.HoldID,
				pendingTemplate.HoldAmount, pendingTemplate.RequestPayloadHash,
			); reserveErr != nil {
				if errors.Is(reserveErr, service.ErrBatchImageInsufficientBalance) {
					h.errorResponse(c, http.StatusPaymentRequired, "insufficient_balance", "Insufficient balance for Seedance video generation")
				} else {
					h.errorResponse(c, http.StatusServiceUnavailable, "billing_hold_failed", "Failed to reserve balance for Seedance video generation")
				}
				return
			}
			holdReserved = true
		}
	}

	sessionSeed := body
	if endpoint.IsLookup() {
		sessionSeed = []byte(taskID)
	}
	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, sessionSeed)
	boundAccountID := int64(0)
	taskProviderID := ""
	if endpoint.IsLookup() {
		sessionHash = service.SeedanceVideoTaskSessionHash(taskID, subject.UserID, apiKey.ID)
		pending, loadErr := h.gatewayService.LoadSeedanceVideoPendingBilling(c.Request.Context(), taskID, subject.UserID, apiKey.ID)
		if loadErr != nil || pending == nil {
			h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
			return
		}
		taskProviderID = pending.ProviderID
		boundAccountID, err = h.gatewayService.ResolveSeedanceVideoTaskAccount(c.Request.Context(), apiKey.GroupID, taskID, subject.UserID, apiKey.ID)
		if err != nil || boundAccountID <= 0 {
			h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
			return
		}
	}

	requestCtx := service.WithOpenAIProfitControlSuppressed(c.Request.Context())
	failedAccountIDs := make(map[int64]struct{})
	maxSwitches := h.maxAccountSwitches
	if maxSwitches <= 0 {
		maxSwitches = 3
	}
	switchCount := 0
	routingStart := time.Now()
	var lastFailoverErr *service.UpstreamFailoverError

	for {
		var selection *service.AccountSelectionResult
		var decision service.OpenAIAccountScheduleDecision
		var selectErr error
		if endpoint.IsLookup() {
			selection, decision, selectErr = h.gatewayService.SelectSeedanceVideoTaskAccount(
				requestCtx, apiKey.GroupID, boundAccountID,
			)
		} else {
			selection, decision, selectErr = h.gatewayService.SelectAccountWithSchedulerForCapability(
				requestCtx, apiKey.GroupID, "", sessionHash, requestModel, failedAccountIDs,
				service.OpenAIUpstreamTransportHTTPSSE, "", false, false, false, service.PlatformSeedance,
			)
		}
		if selectErr != nil || selection == nil || selection.Account == nil {
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				markOpsRoutingCapacityLimitedIfNoAvailable(c, selectErr)
				h.errorResponse(c, http.StatusServiceUnavailable, "seedance_no_available_account", "No available Seedance accounts")
			}
			return
		}
		account := selection.Account
		if endpoint == service.SeedanceVideoEndpointCreate {
			taskProviderID = string(account.GetVideoProviderID())
			if assignErr := h.gatewayService.AssignSeedanceVideoTaskAccount(requestCtx, &pendingTemplate, account.ID, taskProviderID); assignErr != nil {
				reqLog.Error("seedance_video.assign_task_account_failed", zap.Error(assignErr), zap.Int64("account_id", account.ID))
				h.errorResponse(c, http.StatusServiceUnavailable, "seedance_state_persistence_failed",
					"Failed to persist Seedance account assignment")
				return
			}
		}
		reqLog.Debug("seedance_video.account_schedule_decision",
			zap.String("layer", decision.Layer), zap.Bool("sticky_session_hit", decision.StickySessionHit),
			zap.Int("candidate_count", decision.CandidateCount), zap.Int64("account_id", account.ID))
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountRelease, slotResult := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, false, &streamStarted, reqLog)
		if slotResult != openAISlotAcquireOK {
			return
		}
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		result, forwardErr := func() (*service.SeedanceVideoForwardResult, error) {
			defer func() {
				if accountRelease != nil {
					accountRelease()
				}
			}()
			return h.gatewayService.ForwardSeedanceVideo(requestCtx, c, account, taskProviderID, endpoint, taskID, createRequest)
		}()
		if forwardErr != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(forwardErr, &failoverErr) {
				if failoverErr.ShouldReportAccountScheduleFailure() {
					h.gatewayService.ReportOpenAIAccountScheduleResult(account, requestModel, false, nil)
				}
				if endpoint.IsLookup() || !failoverErr.ShouldRetryNextAccount() || switchCount >= maxSwitches {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				h.gatewayService.RecordOpenAIAccountSwitch()
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				switchCount++
				continue
			}
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, requestModel, false, nil)
			if !service.IsResponseCommitted(c) {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			return
		}
		h.gatewayService.ReportOpenAIAccountScheduleResult(account, requestModel, true, nil)
		if endpoint == service.SeedanceVideoEndpointCreate {
			taskID = strings.TrimSpace(result.ResponseID)
			if taskID == "" {
				reqLog.Error("seedance_video.create_response_missing_task_id")
				return
			}
			pending := pendingTemplate
			pending.AccountID = account.ID
			if err := h.gatewayService.StoreSeedanceVideoPendingBilling(requestCtx, taskID, subject.UserID, apiKey.ID, pending); err != nil {
				reqLog.Error("seedance_video.store_pending_billing_failed", zap.Error(err), zap.String("task_id", taskID))
				h.errorResponse(c, http.StatusServiceUnavailable, "seedance_state_persistence_failed",
					"Video task was created upstream but its local state could not be persisted; do not retry automatically")
				return
			}
			taskNeedsRelease = false
			holdReserved = false
			if err := h.gatewayService.BindSeedanceVideoTaskAccount(requestCtx, apiKey.GroupID, taskID, subject.UserID, apiKey.ID, account.ID); err != nil {
				reqLog.Warn("seedance_video.bind_task_account_failed_using_pending_fallback", zap.Error(err), zap.String("task_id", taskID))
			}
			if err := h.gatewayService.CommitSeedanceVideoResponse(c, result); err != nil {
				reqLog.Error("seedance_video.commit_create_response_failed", zap.Error(err), zap.String("task_id", taskID))
				if !service.IsResponseCommitted(c) {
					h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to commit video task response")
				}
			}
			return
		}
		if service.IsSeedanceVideoFailedStatus(result.TaskStatus) {
			releaseFailedSeedanceVideoBilling(requestCtx, h, reqLog, apiKey, subject, taskID)
			return
		}
		if billing := prepareSeedanceVideoCompletionBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result.OpenAIForwardResult); billing != nil {
			recordSeedanceVideoUsage(c, h, reqLog, apiKey, subject, subscription, account, billing)
		}
		return
	}
}

type seedanceVideoCompletionBilling struct {
	Result  *service.OpenAIForwardResult
	Cost    *service.CostBreakdown
	Pending *service.SeedanceVideoPendingBilling
}

func prepareSeedanceVideoCompletionBilling(ctx context.Context, h *OpenAIGatewayHandler, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, taskID string, status *service.OpenAIForwardResult) *seedanceVideoCompletionBilling {
	if h == nil || h.gatewayService == nil || apiKey == nil || status == nil || status.VideoCount <= 0 {
		return nil
	}
	pending, err := h.gatewayService.LoadSeedanceVideoPendingBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if err != nil || pending == nil {
		reqLog.Error("seedance_video.pending_billing_missing", zap.String("task_id", taskID), zap.Error(err))
		return nil
	}
	claimed, err := h.gatewayService.ClaimSeedanceVideoBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if err != nil || !claimed {
		return nil
	}
	merged, cost, buildErr := service.BuildSeedanceVideoCompletionBilling(pending, status)
	if buildErr != nil {
		_ = h.gatewayService.ReleaseSeedanceVideoBilling(ctx, taskID, subject.UserID, apiKey.ID)
		reqLog.Error("seedance_video.completion_billing_invalid", zap.String("task_id", taskID), zap.Error(buildErr))
		return nil
	}
	return &seedanceVideoCompletionBilling{Result: merged, Cost: cost, Pending: pending}
}

func releaseFailedSeedanceVideoBilling(ctx context.Context, h *OpenAIGatewayHandler, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, taskID string) {
	pending, err := h.gatewayService.LoadSeedanceVideoPendingBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if err != nil || pending == nil {
		return
	}
	claimed, err := h.gatewayService.ClaimSeedanceVideoBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if err != nil || !claimed {
		return
	}
	if err := h.gatewayService.ReleaseSeedanceVideoBalance(ctx, pending); err != nil {
		_ = h.gatewayService.ReleaseSeedanceVideoBilling(ctx, taskID, subject.UserID, apiKey.ID)
		reqLog.Error("seedance_video.failed_hold_release_failed", zap.Error(err), zap.String("task_id", taskID))
		return
	}
	if err := h.gatewayService.ReleaseSeedanceVideoTask(ctx, pending); err != nil {
		reqLog.Warn("seedance_video.failed_task_release_failed", zap.Error(err), zap.String("task_id", taskID))
	}
}

func recordSeedanceVideoUsage(c *gin.Context, h *OpenAIGatewayHandler, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, subscription *service.UserSubscription, account *service.Account, billing *seedanceVideoCompletionBilling) {
	if billing == nil || billing.Result == nil || billing.Cost == nil || billing.Pending == nil {
		return
	}
	result, pending := billing.Result, billing.Pending
	if pending.IsSubscriptionBilling && subscription == nil {
		_ = h.gatewayService.ReleaseSeedanceVideoBilling(c.Request.Context(), pending.TaskID, subject.UserID, apiKey.ID)
		return
	}
	result.RequestID = service.StableSeedanceVideoBillingRequestID(pending.TaskID)
	result.ResponseID = pending.TaskID
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	channelUsageFields := service.ChannelUsageFields{
		OriginalModel:      clientRequestedModel(c, result.Model),
		ChannelMappedModel: result.Model,
	}
	h.submitOpenAIUsageRecordTask(c.Request.Context(), result, func(ctx context.Context) {
		subscriptionBilling := pending.IsSubscriptionBilling
		rateMultiplier := pending.RateMultiplier
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result: result, APIKey: apiKey, User: apiKey.User, Account: account,
			Subscription: subscription, InboundEndpoint: inboundEndpoint, UpstreamEndpoint: upstreamEndpoint,
			UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
			RequestPayloadHash: pending.RequestPayloadHash, CostOverride: billing.Cost,
			RateMultiplierOverride: &rateMultiplier, BalanceHoldID: pending.HoldID, BalanceHoldAmount: pending.HoldAmount,
			SubscriptionBillingOverride: &subscriptionBilling, APIKeyService: h.apiKeyService,
			QuotaPlatform: service.PlatformSeedance,
			SessionID:     service.ExtractClientSessionID(c), ChannelUsageFields: channelUsageFields,
		}); err != nil {
			if releaseErr := h.gatewayService.ReleaseSeedanceVideoBilling(ctx, pending.TaskID, subject.UserID, apiKey.ID); releaseErr != nil {
				reqLog.Warn("seedance_video.billing_claim_release_failed", zap.Error(releaseErr))
			}
			logger.L().Error("seedance_video.record_usage_failed", zap.Error(err), zap.String("task_id", pending.TaskID))
			return
		}
		if err := h.gatewayService.CompleteSeedanceVideoTask(ctx, pending, billing.Cost.ActualCost); err != nil {
			reqLog.Warn("seedance_video.task_settlement_complete_failed", zap.Error(err), zap.String("task_id", pending.TaskID))
		}
	})
}
