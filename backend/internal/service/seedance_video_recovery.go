package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"go.uber.org/zap"
)

const (
	seedanceRecoveryPollInterval = 3 * time.Second
	seedanceRecoveryTaskLease    = 30 * time.Second
	seedanceRecoveryRetryDelay   = 5 * time.Second
	seedanceRecoveryBatchSize    = 20
	seedanceRecoveryMaxAge       = 6 * 24 * time.Hour
)

// SeedanceVideoRecoveryService settles tasks even when the client never polls.
// PostgreSQL owns task leases, while usage_billing_dedup remains the money-event
// source of truth, so multiple application replicas may run this worker safely.
type SeedanceVideoRecoveryService struct {
	gateway     *OpenAIGatewayService
	apiKeyRepo  APIKeyRepository
	accountRepo AccountRepository
	subRepo     UserSubscriptionRepository
	apiKeySvc   *APIKeyService
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func NewSeedanceVideoRecoveryService(
	gateway *OpenAIGatewayService,
	apiKeyRepo APIKeyRepository,
	accountRepo AccountRepository,
	subRepo UserSubscriptionRepository,
	apiKeySvc *APIKeyService,
) *SeedanceVideoRecoveryService {
	return &SeedanceVideoRecoveryService{
		gateway: gateway, apiKeyRepo: apiKeyRepo, accountRepo: accountRepo,
		subRepo: subRepo, apiKeySvc: apiKeySvc, stopCh: make(chan struct{}),
	}
}

func ProvideSeedanceVideoRecoveryService(
	gateway *OpenAIGatewayService,
	taskRepo SeedanceVideoTaskRepository,
	apiKeyRepo APIKeyRepository,
	accountRepo AccountRepository,
	subRepo UserSubscriptionRepository,
	apiKeySvc *APIKeyService,
) *SeedanceVideoRecoveryService {
	gateway.SetSeedanceVideoTaskRepository(taskRepo)
	svc := NewSeedanceVideoRecoveryService(gateway, apiKeyRepo, accountRepo, subRepo, apiKeySvc)
	svc.Start()
	return svc
}

func (s *SeedanceVideoRecoveryService) Start() {
	if s == nil || s.gateway == nil {
		return
	}
	if s.gateway.seedanceVideoTaskRepo == nil {
		return
	}
	s.wg.Add(1)
	go s.run()
}

func (s *SeedanceVideoRecoveryService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *SeedanceVideoRecoveryService) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(seedanceRecoveryPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.processDue(context.Background())
		}
	}
}

func (s *SeedanceVideoRecoveryService) processDue(ctx context.Context) {
	claimCtx, claimCancel := context.WithTimeout(ctx, seedanceRecoveryTaskLease)
	tasks, err := s.gateway.ClaimDueSeedanceVideoTasks(claimCtx, time.Now(), seedanceRecoveryTaskLease, seedanceRecoveryBatchSize)
	claimCancel()
	if err != nil {
		logger.L().Warn("seedance_video.recovery_claim_failed", zap.Error(err))
		return
	}
	for _, pending := range tasks {
		wasCancellationRequested := strings.EqualFold(
			strings.TrimSpace(pending.UpstreamStatus), SeedanceVideoCancellationRequestedStatus,
		)
		taskCtx, taskCancel := context.WithTimeout(ctx, seedanceRecoveryTaskLease)
		err := s.processTask(taskCtx, pending)
		taskCancel()
		if err != nil {
			logger.L().Warn("seedance_video.recovery_task_failed",
				zap.String("task_id", pending.TaskID), zap.Error(err))
			// A terminal release intent (or a durable cancellation request) must
			// remain in processing until its owner lease expires. Rescheduling it
			// here would erase the intent and make a later pass poll/settle a task
			// whose balance may already have been released.
			if IsSeedanceVideoReleaseIntentStatus(pending.UpstreamStatus) ||
				strings.EqualFold(strings.TrimSpace(pending.UpstreamStatus), SeedanceVideoCancellationRequestedStatus) {
				continue
			}
			// Cancellation transport/provider errors clear the durable intent and
			// reschedule from processTask itself. Do not issue a second mutation
			// against the same lease when that owner-aware reschedule also returns
			// an error after changing the in-memory snapshot.
			if wasCancellationRequested && !strings.EqualFold(
				strings.TrimSpace(pending.UpstreamStatus), SeedanceVideoCancellationRequestedStatus,
			) {
				continue
			}
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.gateway.RescheduleSeedanceVideoTaskWithError(
				retryCtx, pending, time.Now().Add(seedanceRecoveryRetryDelay), err.Error(),
			)
			retryCancel()
		}
	}
}

func (s *SeedanceVideoRecoveryService) processTask(ctx context.Context, pending *SeedanceVideoPendingBilling) error {
	if pending == nil {
		return errors.New("seedance recovery task is invalid")
	}
	// The terminal status is durable intent, not a fresh provider observation.
	// Retry release directly before looking up an account or making any upstream
	// request; this is the critical guard after a balance release whose final
	// state mutation failed.
	if IsSeedanceVideoReleaseIntentStatus(pending.UpstreamStatus) {
		return s.releaseFailed(ctx, pending, NormalizeSeedanceVideoReleaseStatus(pending.UpstreamStatus))
	}
	createdAt, createdAtErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(pending.CreatedAt))
	if strings.TrimSpace(pending.TaskID) == "" {
		if createdAtErr == nil && time.Since(createdAt) < seedanceVideoCreatingTTL {
			return s.gateway.RescheduleSeedanceVideoTask(ctx, pending, createdAt.Add(seedanceVideoCreatingTTL))
		}
		logger.L().Error("seedance_video.recovery_creating_task_expired_releasing_hold",
			zap.String("state_id", pending.StateID), zap.String("created_at", pending.CreatedAt))
		return s.releaseFailed(ctx, pending, SeedanceVideoResponseStatusFailed)
	}
	if createdAtErr == nil && time.Since(createdAt) >= seedanceRecoveryMaxAge {
		logger.L().Error("seedance_video.recovery_task_expired_releasing_hold",
			zap.String("task_id", pending.TaskID), zap.String("created_at", pending.CreatedAt))
		return s.releaseFailed(ctx, pending, SeedanceVideoResponseStatusFailed)
	}
	account, err := s.accountRepo.GetByID(ctx, pending.AccountID)
	if err != nil || account == nil || !account.IsSeedance() {
		return errors.New("seedance recovery account is unavailable")
	}
	// A cancellation claim is durable intent. If the request process died after
	// claiming but before finalizing, retry the provider cancellation rather than
	// polling and potentially charging a task that the caller asked to cancel.
	if strings.EqualFold(strings.TrimSpace(pending.UpstreamStatus), SeedanceVideoCancellationRequestedStatus) {
		forward, cancelErr := s.gateway.ForwardSeedanceVideo(
			ctx, nil, account, pending.ProviderID, SeedanceVideoEndpointCancel, pending.TaskID, nil,
		)
		if cancelErr != nil {
			// A capability gap means no upstream request was sent. Clear the durable
			// cancellation intent and resume polling; retaining cancel_requested would
			// make every recovery pass retry an operation the provider can never do.
			if errors.Is(cancelErr, videoprovider.ErrVideoTaskCancellationUnsupported) {
				pending.UpstreamStatus = SeedanceVideoResponseStatusInProgress
				return s.gateway.RescheduleSeedanceVideoTaskWithError(
					ctx, pending, time.Now().Add(seedanceRecoveryRetryDelay), cancelErr.Error())
			}
			// A transport or provider error does not tell us whether DELETE took
			// effect. Do not keep the row in cancel_requested indefinitely: the
			// next status poll is the only provider-neutral way to distinguish an
			// eventual cancellation from a task that must still be billed.
			pending.UpstreamStatus = SeedanceVideoResponseStatusInProgress
			return s.gateway.RescheduleSeedanceVideoTaskWithError(
				ctx, pending, time.Now().Add(seedanceRecoveryRetryDelay), cancelErr.Error())
		}
		if forward == nil {
			return errors.New("seedance cancellation returned no result")
		}
		switch normalized := NormalizeSeedanceVideoStatus(forward.TaskStatus); normalized {
		case SeedanceVideoResponseStatusFailed:
			// Preserve an explicit provider cancellation failure. Releasing the
			// hold is still required, but the durable/public state must not claim
			// that cancellation succeeded.
			pending.UpstreamStatus = normalized
			return s.releaseFailed(ctx, pending, SeedanceVideoResponseStatusFailed)
		case SeedanceVideoResponseStatusCompleted:
			// DELETE raced with completion. Settle the output normally instead of
			// releasing the hold as canceled; otherwise a completed video could be
			// served without accounting.
			pending.UpstreamStatus = normalized
			return s.settleCompleted(ctx, pending, account, forward.OpenAIForwardResult)
		default:
			pending.UpstreamStatus = SeedanceVideoResponseStatusCanceled
			return s.releaseFailed(ctx, pending, SeedanceVideoResponseStatusCanceled)
		}
	}
	forward, err := s.gateway.FetchSeedanceVideoStatus(ctx, account, pending.ProviderID, pending.TaskID)
	if err != nil {
		return err
	}
	pending.UpstreamStatus = strings.ToLower(strings.TrimSpace(forward.TaskStatus))
	if IsSeedanceVideoPendingStatus(forward.TaskStatus) || strings.TrimSpace(forward.TaskStatus) == "" {
		return s.gateway.RescheduleSeedanceVideoTask(ctx, pending, time.Now().Add(seedanceRecoveryRetryDelay))
	}
	if IsSeedanceVideoFailedStatus(forward.TaskStatus) {
		terminalStatus := SeedanceVideoResponseStatusFailed
		if NormalizeSeedanceVideoStatus(forward.TaskStatus) == SeedanceVideoResponseStatusCanceled {
			terminalStatus = SeedanceVideoResponseStatusCanceled
		}
		return s.releaseFailed(ctx, pending, terminalStatus)
	}
	if !strings.EqualFold(forward.TaskStatus, "completed") || forward.VideoCount <= 0 {
		return s.gateway.RescheduleSeedanceVideoTask(ctx, pending, time.Now().Add(seedanceRecoveryRetryDelay))
	}
	return s.settleCompleted(ctx, pending, account, forward.OpenAIForwardResult)
}

func (s *SeedanceVideoRecoveryService) releaseFailed(ctx context.Context, pending *SeedanceVideoPendingBilling, terminalStatus string) error {
	terminalStatus = NormalizeSeedanceVideoReleaseStatus(terminalStatus)
	if err := s.gateway.PersistSeedanceVideoReleaseIntent(ctx, pending, terminalStatus); err != nil {
		return err
	}
	if err := s.gateway.ReleaseSeedanceVideoBalance(ctx, pending); err != nil {
		return err
	}
	// A recovery release is terminal even when the last provider observation was
	// queued/running (for example after max age or a task that never received an
	// upstream id). Preserve a provider cancellation as canceled; all other
	// release paths are normalized to failed so later polls cannot resurrect the
	// task from a provider completion.
	return s.gateway.ReleaseSeedanceVideoTaskWithStatus(ctx, pending, terminalStatus)
}

func (s *SeedanceVideoRecoveryService) settleCompleted(ctx context.Context, pending *SeedanceVideoPendingBilling, account *Account, status *OpenAIForwardResult) error {
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, status)
	if err != nil {
		return err
	}
	apiKey, err := s.apiKeyRepo.GetByID(ctx, pending.APIKeyID)
	if err != nil || apiKey == nil || apiKey.User == nil {
		return errors.New("seedance recovery API key is unavailable")
	}
	var subscription *UserSubscription
	if pending.IsSubscriptionBilling {
		if pending.SubscriptionID == nil || s.subRepo == nil {
			return errors.New("seedance recovery subscription snapshot is unavailable")
		}
		subscription, err = s.subRepo.GetByIDIncludeDeleted(ctx, *pending.SubscriptionID)
		if err != nil || subscription == nil {
			return errors.New("seedance recovery subscription is unavailable")
		}
	}
	rateMultiplier := pending.RateMultiplier
	subscriptionBilling := pending.IsSubscriptionBilling
	if err := s.gateway.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: result, APIKey: apiKey, User: apiKey.User, Account: account, Subscription: subscription,
		InboundEndpoint: "/v1/videos", UpstreamEndpoint: "/v1/videos/{task_id}",
		RequestPayloadHash: pending.RequestPayloadHash, CostOverride: cost,
		RateMultiplierOverride: &rateMultiplier, BalanceHoldID: pending.HoldID,
		BalanceHoldAmount: pending.HoldAmount, SubscriptionBillingOverride: &subscriptionBilling,
		APIKeyService: s.apiKeySvc, QuotaPlatform: PlatformSeedance,
		ChannelUsageFields: ChannelUsageFields{OriginalModel: pending.OriginalModel, ChannelMappedModel: pending.Model},
	}); err != nil {
		return err
	}
	if err := s.gateway.CompleteSeedanceVideoTask(ctx, pending, cost.ActualCost); err != nil {
		return err
	}
	return nil
}
