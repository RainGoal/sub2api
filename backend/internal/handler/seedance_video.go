package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
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
	var lookupPending *service.SeedanceVideoPendingBilling
	cancellationClaimed := false
	cancellationFinalized := false
	cancellationForwardAttempted := false
	cancellationPreviousStatus := ""
	reqLog := requestLogger(c, "handler.openai_gateway.seedance_video",
		zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID), zap.String("endpoint", string(endpoint)))
	defer func() {
		if !cancellationClaimed || cancellationFinalized || cancellationForwardAttempted || lookupPending == nil {
			return
		}
		rollbackPending := *lookupPending
		rollbackStatus := strings.TrimSpace(cancellationPreviousStatus)
		if rollbackStatus == "" {
			rollbackStatus = "queued"
		}
		rollbackPending.UpstreamStatus = rollbackStatus
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := h.gatewayService.RescheduleSeedanceVideoTaskWithError(
			rollbackCtx, &rollbackPending, time.Now().Add(5*time.Second), "cancellation was not finalized"); rollbackErr != nil {
			reqLog.Warn("seedance_video.cancel_claim_rollback_failed", zap.Error(rollbackErr), zap.String("task_id", rollbackPending.TaskID))
		}
	}()
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
		lookupPending = pending
		if endpoint == service.SeedanceVideoEndpointContent {
			if unavailable, code, message := seedanceVideoContentUnavailable(lookupPending); unavailable {
				h.errorResponse(c, http.StatusConflict, code, message)
				return
			}
		}
		if localResult, ok := seedanceVideoLocalTerminalResult(endpoint, taskID, lookupPending); ok {
			writeSeedanceVideoResponse(c, http.StatusOK, localResult,
				seedanceVideoResponseMeta(taskID, localResult, lookupPending, requestInfo))
			return
		}
		if endpoint == service.SeedanceVideoEndpointCancel {
			// A live processing lease belongs to another request/worker and must
			// not be stolen. Once the lease has expired, the cancellation claim
			// below is allowed to fence the abandoned owner and retry safely.
			if lookupPending.SettlementStatus == service.SeedanceVideoSettlementProcessing &&
				seedanceVideoLeaseIsActive(lookupPending) {
				h.errorResponse(c, http.StatusConflict, "video_cancellation_in_progress",
					"Video request is being finalized; retry cancellation")
				return
			}
			cancellationPreviousStatus = lookupPending.UpstreamStatus
			claimed, claimErr := h.gatewayService.ClaimSeedanceVideoCancellation(
				c.Request.Context(), taskID, subject.UserID, apiKey.ID)
			if claimErr != nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "seedance_state_persistence_failed",
					"Failed to reserve video cancellation")
				return
			}
			if !claimed {
				current, currentErr := h.gatewayService.LoadSeedanceVideoPendingBilling(
					c.Request.Context(), taskID, subject.UserID, apiKey.ID)
				if currentErr == nil {
					if localResult, ok := seedanceVideoLocalTerminalResult(endpoint, taskID, current); ok {
						writeSeedanceVideoResponse(c, http.StatusOK, localResult,
							seedanceVideoResponseMeta(taskID, localResult, current, requestInfo))
						return
					}
				}
				h.errorResponse(c, http.StatusConflict, "video_cancellation_in_progress",
					"Video request is being finalized; retry cancellation")
				return
			}
			cancellationClaimed = true
			// ClaimSettlement/ClaimCancellation return only a boolean. Reload the
			// row immediately so every subsequent mutation carries the exact lease
			// timestamp assigned by PostgreSQL rather than the pre-claim snapshot.
			claimedPending, claimedLoadErr := h.gatewayService.LoadSeedanceVideoPendingBilling(
				c.Request.Context(), taskID, subject.UserID, apiKey.ID)
			if claimedLoadErr != nil || claimedPending == nil || claimedPending.LeaseUntil == nil || claimedPending.LeaseUntil.IsZero() {
				// We cannot safely roll back a claim without its owner token. Leave
				// the row for lease expiry/recovery instead of issuing an unguarded
				// mutation with the stale snapshot.
				lookupPending = nil
				reqLog.Warn("seedance_video.cancel_claim_reload_failed", zap.Error(claimedLoadErr), zap.String("task_id", taskID))
				h.errorResponse(c, http.StatusServiceUnavailable, "seedance_state_persistence_failed",
					"Failed to load the video cancellation lease")
				return
			}
			lookupPending = claimedPending
			pending = claimedPending
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
		if endpoint == service.SeedanceVideoEndpointCancel {
			// A local driver/build error is safe to roll back. Once the request
			// reached the transport (or an upstream response was received), keep
			// the claim for recovery because the provider may have acted despite a
			// lost/invalid response.
			cancellationForwardAttempted = seedanceVideoCancellationReachedUpstream(c, forwardErr)
		}
		if forwardErr != nil {
			if errors.Is(forwardErr, videoprovider.ErrVideoTaskCancellationUnsupported) {
				// No provider request was sent for this capability error, so the
				// local claim can be rolled back immediately for a retry.
				cancellationForwardAttempted = false
				h.errorResponse(c, http.StatusNotImplemented, "operation_not_supported",
					"Video cancellation is not supported by the configured provider")
				return
			}
			if errors.Is(forwardErr, service.ErrSeedanceVideoContentNotReady) {
				h.errorResponse(c, http.StatusConflict, "video_content_unavailable",
					"Video content is not available yet; retry after the task is completed")
				return
			}
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
				if !service.IsResponseCommitted(c) {
					h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream video task ID is missing")
				}
				return
			}
			pending := pendingTemplate
			pending.AccountID = account.ID
			// Preserve the provider-neutral task state in the durable snapshot. A
			// provider can reject a task synchronously even though the create
			// contract is asynchronous; storing that terminal state prevents the
			// recovery worker from treating it as queued and holding the balance.
			pending.UpstreamStatus = strings.ToLower(strings.TrimSpace(result.TaskStatus))
			if pending.UpstreamStatus == "" {
				pending.UpstreamStatus = "queued"
			}
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
			if service.IsSeedanceVideoFailedStatus(result.TaskStatus) {
				if !releaseTerminalSeedanceVideoBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result.TaskStatus) {
					setSeedanceVideoReleasePendingStatus(result)
				}
			}
			writeSeedanceVideoResponse(c, http.StatusAccepted, result, seedanceVideoResponseMeta(taskID, result, &pending, requestInfo))
			return
		}
		if endpoint == service.SeedanceVideoEndpointCancel {
			cancelStatus := service.NormalizeSeedanceVideoStatus(result.TaskStatus)
			if cancelStatus == service.SeedanceVideoResponseStatusCompleted {
				// The provider explicitly says the task completed before DELETE
				// took effect. Do not release the hold as canceled; hand the task
				// back to the normal status/settlement path and return a stable
				// conflict to the caller.
				lookupPending.UpstreamStatus = service.SeedanceVideoResponseStatusCompleted
				if err := h.gatewayService.RescheduleSeedanceVideoTaskWithError(
					requestCtx, lookupPending, time.Now().Add(2*time.Second),
					"cancellation was not accepted because the task is already completed",
				); err != nil {
					reqLog.Warn("seedance_video.cancel_completed_reschedule_failed", zap.Error(err), zap.String("task_id", taskID))
					h.errorResponse(c, http.StatusServiceUnavailable, "seedance_state_persistence_failed",
						"Video cancellation could not be finalized; retry the request")
					return
				}
				cancellationFinalized = true
				h.errorResponse(c, http.StatusConflict,
					service.SeedanceVideoResponseErrorCodeCancellationConflict,
					service.SeedanceVideoResponseErrorMessageCancellationConflict)
				return
			}
			terminalStatus := service.SeedanceVideoResponseStatusCanceled
			cancellationFailed := cancelStatus == service.SeedanceVideoResponseStatusFailed
			if cancellationFailed {
				terminalStatus = service.SeedanceVideoResponseStatusFailed
			}
			if err := finalizeSeedanceVideoCancellation(requestCtx, h, reqLog, taskID, lookupPending, terminalStatus); err != nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "seedance_state_persistence_failed",
					"Video cancellation was acknowledged but local state could not be finalized")
				return
			}
			cancellationFinalized = true
			if finalPending, loadErr := h.gatewayService.LoadSeedanceVideoPendingBilling(
				requestCtx, taskID, subject.UserID, apiKey.ID); loadErr == nil {
				if localResult, ok := seedanceVideoLocalTerminalResult(endpoint, taskID, finalPending); ok {
					responseMeta := seedanceVideoResponseMeta(taskID, localResult, finalPending, requestInfo)
					if cancellationFailed {
						responseMeta.ErrorCode = service.SeedanceVideoResponseErrorCodeCancellationFailed
						responseMeta.ErrorMessage = service.SeedanceVideoResponseErrorMessageCancellationFailed
					}
					writeSeedanceVideoResponse(c, http.StatusOK, localResult, responseMeta)
					return
				}
			}
			responseMeta := seedanceVideoResponseMeta(taskID, result, lookupPending, requestInfo)
			if cancellationFailed {
				responseMeta.ErrorCode = service.SeedanceVideoResponseErrorCodeCancellationFailed
				responseMeta.ErrorMessage = service.SeedanceVideoResponseErrorMessageCancellationFailed
			}
			writeSeedanceVideoResponse(c, http.StatusOK, result, responseMeta)
			return
		}
		terminalReleaseSucceeded := true
		if service.IsSeedanceVideoFailedStatus(result.TaskStatus) {
			terminalReleaseSucceeded = releaseTerminalSeedanceVideoBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result.TaskStatus)
			if !terminalReleaseSucceeded {
				setSeedanceVideoReleasePendingStatus(result)
			}
		} else if billing := prepareSeedanceVideoCompletionBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result.OpenAIForwardResult); billing != nil {
			recordSeedanceVideoUsage(c, h, reqLog, apiKey, subject, subscription, account, billing)
		}
		if endpoint == service.SeedanceVideoEndpointStatus {
			// The task may have been claimed by cancellation or another status
			// request while the provider call was in flight. Re-read the durable
			// state so the response cannot report a provider completion that local
			// accounting has already superseded.
			if terminalReleaseSucceeded {
				if current, loadErr := h.gatewayService.LoadSeedanceVideoPendingBilling(
					requestCtx, taskID, subject.UserID, apiKey.ID); loadErr == nil {
					if localResult, ok := seedanceVideoLocalTerminalResult(endpoint, taskID, current); ok {
						result = localResult
						lookupPending = current
					}
				}
			}
		}
		if endpoint != service.SeedanceVideoEndpointContent {
			writeSeedanceVideoResponse(c, http.StatusOK, result, seedanceVideoResponseMeta(taskID, result, lookupPending, requestInfo))
		}
		return
	}
}

func seedanceVideoLeaseIsActive(pending *service.SeedanceVideoPendingBilling) bool {
	if pending == nil || pending.LeaseUntil == nil || pending.LeaseUntil.IsZero() {
		return false
	}
	return pending.LeaseUntil.After(time.Now())
}

// setSeedanceVideoReleasePendingStatus prevents a provider terminal failure
// from being exposed as durable failed while the local hold/task release is
// still retryable. The recovery worker owns the task until that mutation
// succeeds, so callers should observe in_progress during the window.
func setSeedanceVideoReleasePendingStatus(result *service.SeedanceVideoForwardResult) {
	if result == nil || !service.IsSeedanceVideoFailedStatus(result.TaskStatus) {
		return
	}
	result.TaskStatus = service.SeedanceVideoResponseStatusInProgress
}

func seedanceVideoCancellationReachedUpstream(c *gin.Context, err error) bool {
	if err == nil {
		return true
	}
	if c != nil && service.IsResponseCommitted(c) {
		// The oversized-body path writes an error after receiving an upstream
		// response but returns a plain sentinel rather than UpstreamFailoverError.
		return true
	}
	var failoverErr *service.UpstreamFailoverError
	return errors.As(err, &failoverErr)
}

// writeSeedanceVideoResponse is the sole JSON success writer for Seedance
// create/status/cancel endpoints. Provider response bodies are intentionally
// never forwarded to callers.
func writeSeedanceVideoResponse(c *gin.Context, statusCode int, result *service.SeedanceVideoForwardResult, meta service.SeedanceVideoResponseMeta) {
	if c == nil || c.Writer == nil || c.Writer.Written() || service.IsResponseCommitted(c) {
		return
	}
	response := service.BuildSeedanceVideoResponse(result, meta)
	c.Header("Cache-Control", "no-store")
	c.JSON(statusCode, response)
	service.MarkResponseCommitted(c)
}

func seedanceVideoResponseMeta(taskID string, result *service.SeedanceVideoForwardResult, pending *service.SeedanceVideoPendingBilling, requestInfo service.SeedanceVideoRequestInfo) service.SeedanceVideoResponseMeta {
	meta := service.SeedanceVideoResponseMeta{ID: strings.TrimSpace(taskID)}
	if pending != nil {
		meta.Model = pending.Model
		meta.Resolution = pending.Resolution
		meta.Duration = pending.DurationSeconds
		meta.SettlementStatus = pending.SettlementStatus
		if strings.TrimSpace(meta.SettlementStatus) == "" {
			meta.SettlementStatus = service.SeedanceVideoSettlementPending
		}
	}
	if strings.TrimSpace(meta.Model) == "" {
		meta.Model = requestInfo.Model
	}
	if strings.TrimSpace(meta.Resolution) == "" {
		meta.Resolution = requestInfo.Resolution
	}
	if meta.Duration <= 0 {
		meta.Duration = requestInfo.DurationSeconds
	}
	status := ""
	if result != nil {
		status = service.NormalizeSeedanceVideoStatus(result.TaskStatus)
	}
	if status == service.SeedanceVideoResponseStatusCompleted && meta.ID != "" &&
		strings.EqualFold(meta.SettlementStatus, service.SeedanceVideoSettlementSettled) {
		meta.ContentURL = "/v1/videos/" + url.PathEscape(meta.ID) + "/content"
	}
	if status == service.SeedanceVideoResponseStatusFailed {
		if strings.TrimSpace(meta.ErrorCode) == "" {
			meta.ErrorCode = service.SeedanceVideoResponseErrorCodeGenerationFailed
		}
		if strings.TrimSpace(meta.ErrorMessage) == "" {
			meta.ErrorMessage = service.SeedanceVideoResponseErrorMessageGenerationFailed
		}
	}
	return meta
}

// seedanceVideoLocalTerminalResult makes durable accounting state authoritative
// after a task has been settled or released. A processing row is also kept
// local for status reads: another request is currently finalizing the task and
// must not be raced by a provider poll or cancellation request.
func seedanceVideoLocalTerminalResult(endpoint service.SeedanceVideoEndpoint, taskID string, pending *service.SeedanceVideoPendingBilling) (*service.SeedanceVideoForwardResult, bool) {
	if pending == nil || strings.TrimSpace(taskID) == "" {
		return nil, false
	}
	status := service.NormalizeSeedanceVideoStatus(pending.UpstreamStatus)
	cancellationRequested := strings.EqualFold(strings.TrimSpace(pending.UpstreamStatus), service.SeedanceVideoCancellationRequestedStatus)
	settlement := strings.ToLower(strings.TrimSpace(pending.SettlementStatus))
	localStatus := ""
	switch endpoint {
	case service.SeedanceVideoEndpointStatus:
		switch settlement {
		case service.SeedanceVideoSettlementSettled:
			// A settled row is the billing source of truth and therefore always
			// represents a completed, downloadable task.
			localStatus = service.SeedanceVideoResponseStatusCompleted
		case service.SeedanceVideoSettlementReleased:
			if cancellationRequested || status == service.SeedanceVideoResponseStatusCanceled {
				localStatus = service.SeedanceVideoResponseStatusCanceled
			} else if status == service.SeedanceVideoResponseStatusFailed {
				localStatus = status
			} else {
				// Older rows could be released while still carrying queued/running;
				// released billing state must never be resurrected by a later poll.
				localStatus = service.SeedanceVideoResponseStatusFailed
			}
		case service.SeedanceVideoSettlementProcessing:
			// A provider completion can still be in the local billing commit
			// window. Keep the public state in progress until the row is settled,
			// so every completed response is immediately content-downloadable.
			localStatus = service.SeedanceVideoResponseStatusInProgress
		case service.SeedanceVideoSettlementPending:
			// A pending row still owns an active billing hold. Even if a previous
			// provider poll recorded a terminal status, let the normal handler path
			// claim the row, release the hold, and persist the terminal state before
			// exposing it. Returning early here could strand the hold indefinitely.
		}
	case service.SeedanceVideoEndpointCancel:
		switch settlement {
		case service.SeedanceVideoSettlementSettled:
			localStatus = service.SeedanceVideoResponseStatusCompleted
		case service.SeedanceVideoSettlementReleased:
			if cancellationRequested || status == service.SeedanceVideoResponseStatusCanceled {
				localStatus = service.SeedanceVideoResponseStatusCanceled
			} else if status == service.SeedanceVideoResponseStatusFailed {
				localStatus = status
			} else {
				localStatus = service.SeedanceVideoResponseStatusFailed
			}
		case service.SeedanceVideoSettlementPending:
			// The cancellation claim is acquired below. Do not make a terminal
			// provider status idempotent until its local release is durable.
		case service.SeedanceVideoSettlementProcessing:
			// The caller maps any processing row to a cancellation conflict. This
			// prevents a completion claim from being mistaken for an idempotent
			// cancellation before local billing has settled.
		}
	}
	if localStatus == "" {
		return nil, false
	}
	return seedanceVideoLocalResult(taskID, pending, localStatus), true
}

func seedanceVideoLocalResult(taskID string, pending *service.SeedanceVideoPendingBilling, status string) *service.SeedanceVideoForwardResult {
	videoCount := 0
	if status == service.SeedanceVideoResponseStatusCompleted {
		videoCount = 1
	}
	return &service.SeedanceVideoForwardResult{
		OpenAIForwardResult: &service.OpenAIForwardResult{
			ResponseID: taskID, Model: pending.Model, BillingModel: pending.Model,
			VideoResolution: pending.Resolution, VideoDurationSeconds: pending.DurationSeconds,
			VideoCount: videoCount,
		},
		TaskStatus: status,
	}
}

func seedanceVideoContentUnavailable(pending *service.SeedanceVideoPendingBilling) (bool, string, string) {
	if pending == nil {
		return false, "", ""
	}
	settlement := strings.ToLower(strings.TrimSpace(pending.SettlementStatus))
	// Content is released only after the local accounting transaction is
	// settled. This also blocks provider bytes while a failed settlement is
	// being retried, avoiding an unbilled download.
	if settlement != service.SeedanceVideoSettlementSettled {
		return true, "video_content_unavailable", "Video content is not available for this task"
	}
	return false, "", ""
}

func finalizeSeedanceVideoCancellation(ctx context.Context, h *OpenAIGatewayHandler, reqLog *zap.Logger, taskID string, pending *service.SeedanceVideoPendingBilling, terminalStatus string) error {
	if pending == nil || !strings.EqualFold(strings.TrimSpace(pending.TaskID), strings.TrimSpace(taskID)) {
		return service.ErrSeedanceVideoTaskNotFound
	}
	if pending.SettlementStatus == service.SeedanceVideoSettlementSettled || pending.SettlementStatus == service.SeedanceVideoSettlementReleased {
		return nil
	}
	terminalStatus = service.NormalizeSeedanceVideoReleaseStatus(terminalStatus)
	// The claim snapshot is the only lease token this request is allowed to use.
	// Stop before touching the balance when it is already near expiry; a recovery
	// worker can then retry cancellation with the current owner token.
	if pending.LeaseUntil != nil && time.Until(*pending.LeaseUntil) <= time.Second {
		return service.ErrSeedanceVideoLeaseMutationUnsupported
	}
	if err := h.gatewayService.PersistSeedanceVideoReleaseIntent(
		ctx, pending, terminalStatus,
	); err != nil {
		if reqLog != nil {
			reqLog.Warn("seedance_video.cancel_release_intent_failed", zap.Error(err), zap.String("task_id", taskID))
		}
		return err
	}
	if err := h.gatewayService.ReleaseSeedanceVideoBalance(ctx, pending); err != nil {
		if reqLog != nil {
			reqLog.Warn("seedance_video.cancel_hold_release_failed", zap.Error(err), zap.String("task_id", taskID))
		}
		return err
	}
	return h.gatewayService.ReleaseSeedanceVideoTaskWithStatus(ctx, pending, terminalStatus)
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
	claimedPending, claimedLoadErr := h.gatewayService.LoadSeedanceVideoPendingBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if claimedLoadErr != nil || claimedPending == nil || claimedPending.LeaseUntil == nil || claimedPending.LeaseUntil.IsZero() {
		// Without the post-claim lease token, do not build a billing mutation
		// from the stale pre-claim snapshot. Recovery will reclaim the row after
		// the owner lease expires; do not reload by task ID and risk another owner.
		if reqLog != nil {
			reqLog.Warn("seedance_video.completion_claim_reload_failed", zap.String("task_id", taskID), zap.Error(claimedLoadErr))
		}
		return nil
	}
	pending = claimedPending
	merged, cost, buildErr := service.BuildSeedanceVideoCompletionBilling(pending, status)
	if buildErr != nil {
		if retryErr := h.gatewayService.RetrySeedanceVideoBillingWithLease(ctx, pending, "settlement retry"); retryErr != nil && reqLog != nil {
			reqLog.Warn("seedance_video.completion_billing_retry_schedule_failed", zap.String("task_id", taskID), zap.Error(retryErr))
		}
		reqLog.Error("seedance_video.completion_billing_invalid", zap.String("task_id", taskID), zap.Error(buildErr))
		return nil
	}
	return &seedanceVideoCompletionBilling{Result: merged, Cost: cost, Pending: pending}
}

func releaseTerminalSeedanceVideoBilling(ctx context.Context, h *OpenAIGatewayHandler, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, taskID, terminalStatus string) bool {
	pending, err := h.gatewayService.LoadSeedanceVideoPendingBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if err != nil || pending == nil {
		return false
	}
	claimed, err := h.gatewayService.ClaimSeedanceVideoBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if err != nil || !claimed {
		return false
	}
	claimedPending, claimedLoadErr := h.gatewayService.LoadSeedanceVideoPendingBilling(ctx, taskID, subject.UserID, apiKey.ID)
	if claimedLoadErr != nil || claimedPending == nil || claimedPending.LeaseUntil == nil || claimedPending.LeaseUntil.IsZero() {
		if reqLog != nil {
			reqLog.Warn("seedance_video.terminal_claim_reload_failed", zap.String("task_id", taskID), zap.Error(claimedLoadErr))
		}
		// Keep the release path owner-aware. If the row cannot be reloaded, it
		// remains leased and will be recovered after expiry.
		return false
	}
	pending = claimedPending
	terminalStatus = service.NormalizeSeedanceVideoReleaseStatus(terminalStatus)
	if err := h.gatewayService.PersistSeedanceVideoReleaseIntent(ctx, pending, terminalStatus); err != nil {
		if reqLog != nil {
			reqLog.Warn("seedance_video.failed_release_intent_failed", zap.Error(err), zap.String("task_id", taskID))
		}
		return false
	}
	if err := h.gatewayService.ReleaseSeedanceVideoBalance(ctx, pending); err != nil {
		if reqLog != nil {
			reqLog.Error("seedance_video.failed_hold_release_failed", zap.Error(err), zap.String("task_id", taskID))
		}
		// Keep the processing claim and durable terminal intent. Recovery will
		// retry after the lease expires without polling the provider again.
		return false
	}
	if err := h.gatewayService.ReleaseSeedanceVideoTaskWithStatus(ctx, pending, terminalStatus); err != nil {
		if reqLog != nil {
			reqLog.Warn("seedance_video.failed_task_release_failed", zap.Error(err), zap.String("task_id", taskID))
		}
		return false
	}
	return true
}

func recordSeedanceVideoUsage(c *gin.Context, h *OpenAIGatewayHandler, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, subscription *service.UserSubscription, account *service.Account, billing *seedanceVideoCompletionBilling) {
	if billing == nil || billing.Result == nil || billing.Cost == nil || billing.Pending == nil {
		return
	}
	result, pending := billing.Result, billing.Pending
	if pending.IsSubscriptionBilling && subscription == nil {
		if retryErr := h.gatewayService.RetrySeedanceVideoBillingWithLease(c.Request.Context(), pending, "subscription context unavailable"); retryErr != nil && reqLog != nil {
			reqLog.Warn("seedance_video.subscription_billing_retry_schedule_failed", zap.String("task_id", pending.TaskID), zap.Error(retryErr))
		}
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
			if releaseErr := h.gatewayService.RetrySeedanceVideoBillingWithLease(ctx, pending, "settlement retry"); releaseErr != nil {
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
