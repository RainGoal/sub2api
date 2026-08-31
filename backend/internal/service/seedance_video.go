package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/gin-gonic/gin"
)

type SeedanceVideoEndpoint string

const (
	SeedanceVideoEndpointCreate  SeedanceVideoEndpoint = "create"
	SeedanceVideoEndpointStatus  SeedanceVideoEndpoint = "status"
	SeedanceVideoEndpointContent SeedanceVideoEndpoint = "content"
	SeedanceVideoEndpointCancel  SeedanceVideoEndpoint = "cancel"
)

// ErrSeedanceVideoContentNotReady is returned when the provider reports a
// task that has not reached the completed state. The handler maps it to a
// stable retryable conflict instead of exposing provider bytes prematurely.
var ErrSeedanceVideoContentNotReady = errors.New("seedance video content is not ready")

func (e SeedanceVideoEndpoint) IsLookup() bool {
	return e == SeedanceVideoEndpointStatus || e == SeedanceVideoEndpointContent || e == SeedanceVideoEndpointCancel
}

type SeedanceVideoRequestInfo = videoprovider.RequestInfo
type SeedanceVideoCreateRequest = videoprovider.CreateRequest

// SeedanceVideoForwardResult contains the provider-neutral task result. The
// handler renders the public JSON response after persistence and billing
// bookkeeping have completed.
type SeedanceVideoForwardResult struct {
	*OpenAIForwardResult
	TaskStatus string
}

func PrepareSeedanceVideoRequest(body []byte) (*SeedanceVideoCreateRequest, []byte, SeedanceVideoRequestInfo, error) {
	request, info, err := videoprovider.ParseCreateRequest(body)
	if err != nil {
		return nil, nil, SeedanceVideoRequestInfo{}, err
	}
	normalized, err := request.CanonicalJSON()
	if err != nil {
		return nil, nil, SeedanceVideoRequestInfo{}, err
	}
	return request, normalized, info, nil
}

func SeedanceVideoTaskSessionHash(taskID string, userID, apiKeyID int64) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || userID <= 0 || apiKeyID <= 0 {
		return ""
	}
	return "seedance-video:" + DeriveSessionHashFromSeed(fmt.Sprintf("%d:%d:%s", userID, apiKeyID, taskID))
}

func (s *OpenAIGatewayService) BindSeedanceVideoTaskAccount(ctx context.Context, groupID *int64, taskID string, userID, apiKeyID, accountID int64) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("seedance video task binding cache is unavailable")
	}
	cacheKey := s.openAISessionCacheKey(SeedanceVideoTaskSessionHash(taskID, userID, apiKeyID))
	if cacheKey == "" || accountID <= 0 {
		return fmt.Errorf("seedance video task binding is invalid")
	}
	return s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), cacheKey, accountID, seedanceVideoAffinityTTL)
}

func (s *OpenAIGatewayService) ResolveSeedanceVideoTaskAccount(ctx context.Context, groupID *int64, taskID string, userID, apiKeyID int64) (int64, error) {
	if s == nil {
		return 0, fmt.Errorf("seedance video task binding is unavailable")
	}
	cacheKey := s.openAISessionCacheKey(SeedanceVideoTaskSessionHash(taskID, userID, apiKeyID))
	if cacheKey == "" {
		return 0, fmt.Errorf("seedance video task binding is invalid")
	}
	var cacheErr error
	if s.cache != nil {
		accountID, err := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
		if err == nil && accountID > 0 {
			return accountID, nil
		}
		cacheErr = err
	}
	// The billing snapshot is written before the ordinary sticky binding. It is
	// therefore the durable recovery source if the second Redis write fails.
	pending, pendingErr := s.LoadSeedanceVideoPendingBilling(ctx, taskID, userID, apiKeyID)
	if pendingErr != nil {
		return 0, pendingErr
	}
	if pending != nil && pending.AccountID > 0 {
		if s.cache != nil {
			_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), cacheKey, pending.AccountID, seedanceVideoAffinityTTL)
		}
		return pending.AccountID, nil
	}
	if cacheErr != nil {
		return 0, cacheErr
	}
	return 0, ErrStickySessionNotFound
}

func (s *OpenAIGatewayService) SelectSeedanceVideoTaskAccount(
	ctx context.Context,
	groupID *int64,
	accountID int64,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{Layer: "seedance_task_binding", StickySessionHit: true}
	if s == nil || accountID <= 0 {
		return nil, decision, ErrNoAvailableAccounts
	}
	account, err := s.getSchedulableAccount(ctx, accountID)
	if err != nil {
		return nil, decision, err
	}
	account = s.recheckSelectedOpenAIAccountFromDB(ctx, account, groupID, PlatformSeedance, "", false, "")
	if account == nil || !s.openAIAccountMatchesSchedulingGroup(account, groupID) ||
		!account.IsSeedance() || account.Type != AccountTypeAPIKey {
		return nil, decision, ErrNoAvailableAccounts
	}
	decision.CandidateCount = 1
	decision.SelectedAccountID = account.ID
	decision.SelectedAccountType = account.Type

	acquired, acquireErr := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if acquireErr == nil && acquired != nil && acquired.Acquired {
		return attachSelectionProfitGate(ctx, &AccountSelectionResult{
			Account: account, Acquired: true, ReleaseFunc: acquired.ReleaseFunc,
		}), decision, nil
	}
	if s.concurrencyService != nil {
		cfg := s.schedulingConfig()
		return attachSelectionProfitGate(ctx, &AccountSelectionResult{
			Account: account,
			WaitPlan: &AccountWaitPlan{
				AccountID: account.ID, MaxConcurrency: account.Concurrency,
				Timeout: cfg.StickySessionWaitTimeout, MaxWaiting: cfg.StickySessionMaxWaiting,
			},
		}), decision, nil
	}
	if acquireErr != nil {
		return nil, decision, acquireErr
	}
	return nil, decision, ErrNoAvailableAccounts
}

type SeedanceVideoPendingBilling struct {
	StateID               string     `json:"state_id"`
	TaskID                string     `json:"task_id"`
	UserID                int64      `json:"user_id"`
	APIKeyID              int64      `json:"api_key_id"`
	GroupID               *int64     `json:"group_id,omitempty"`
	AccountID             int64      `json:"account_id"`
	ProviderID            string     `json:"provider_id"`
	Model                 string     `json:"model"`
	Resolution            string     `json:"resolution"`
	DurationSeconds       int        `json:"duration_seconds"`
	ReferenceVideoCount   int        `json:"reference_video_count,omitempty"`
	OriginalModel         string     `json:"original_model,omitempty"`
	CreatedAt             string     `json:"created_at"`
	RequestPayloadHash    string     `json:"request_payload_hash,omitempty"`
	HoldID                string     `json:"hold_id,omitempty"`
	HoldAmount            float64    `json:"hold_amount,omitempty"`
	TotalCostPerSecond    float64    `json:"total_cost_per_second"`
	ActualCostPerSecond   float64    `json:"actual_cost_per_second"`
	RateMultiplier        float64    `json:"rate_multiplier"`
	IsSubscriptionBilling bool       `json:"is_subscription_billing,omitempty"`
	SubscriptionID        *int64     `json:"subscription_id,omitempty"`
	UpstreamStatus        string     `json:"upstream_status,omitempty"`
	SettlementStatus      string     `json:"settlement_status,omitempty"`
	RetryCount            int        `json:"retry_count,omitempty"`
	NextPollAt            time.Time  `json:"next_poll_at,omitempty"`
	LeaseUntil            *time.Time `json:"lease_until,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
}

const (
	seedanceVideoFirstPoll   = 4 * time.Second
	seedanceVideoTaskLease   = 30 * time.Second
	seedanceVideoRetryDelay  = 5 * time.Second
	seedanceVideoCreatingTTL = 2 * time.Minute
	seedanceVideoAffinityTTL = 7 * 24 * time.Hour
)

func normalizeSeedancePendingBilling(pending *SeedanceVideoPendingBilling) error {
	if pending == nil || strings.TrimSpace(pending.StateID) == "" || pending.UserID <= 0 || pending.APIKeyID <= 0 {
		return fmt.Errorf("seedance video pending billing state is invalid")
	}
	pending.Model = strings.TrimSpace(pending.Model)
	pending.OriginalModel = strings.TrimSpace(pending.OriginalModel)
	providerID, err := videoprovider.NormalizeID(pending.ProviderID)
	if err != nil {
		return err
	}
	pending.ProviderID = string(providerID)
	resolution, ok := LookupVideoBillingResolution(pending.Resolution)
	if !ok {
		return fmt.Errorf("seedance video billing resolution is invalid")
	}
	pending.Resolution = resolution
	pending.DurationSeconds = NormalizeVideoBillingDurationForProvider(PlatformSeedance, pending.Model, pending.DurationSeconds)
	if pending.DurationSeconds <= 0 {
		return fmt.Errorf("seedance video billing duration is invalid")
	}
	if strings.TrimSpace(pending.HoldID) == "" {
		return fmt.Errorf("seedance video hold identity is invalid")
	}
	if strings.TrimSpace(pending.CreatedAt) == "" {
		pending.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if pending.NextPollAt.IsZero() {
		pending.NextPollAt = time.Now().Add(seedanceVideoCreatingTTL)
	}
	return nil
}

func (s *OpenAIGatewayService) BeginSeedanceVideoTask(ctx context.Context, pending *SeedanceVideoPendingBilling) error {
	if s == nil || s.seedanceVideoTaskRepo == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	if err := normalizeSeedancePendingBilling(pending); err != nil {
		return err
	}
	return s.seedanceVideoTaskRepo.Create(ctx, pending)
}

func (s *OpenAIGatewayService) AssignSeedanceVideoTaskAccount(ctx context.Context, pending *SeedanceVideoPendingBilling, accountID int64, providerID string) error {
	if s == nil || s.seedanceVideoTaskRepo == nil || pending == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	normalizedProviderID, err := videoprovider.NormalizeID(providerID)
	if err != nil {
		return err
	}
	if err := s.seedanceVideoTaskRepo.AssignAccount(ctx, pending.StateID, accountID, string(normalizedProviderID)); err != nil {
		return err
	}
	pending.AccountID = accountID
	pending.ProviderID = string(normalizedProviderID)
	return nil
}

func (s *OpenAIGatewayService) StoreSeedanceVideoPendingBilling(ctx context.Context, taskID string, userID, apiKeyID int64, pending SeedanceVideoPendingBilling) error {
	if s == nil || s.seedanceVideoTaskRepo == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	pending.TaskID = strings.TrimSpace(taskID)
	pending.UserID = userID
	pending.APIKeyID = apiKeyID
	if pending.TaskID == "" || pending.AccountID <= 0 {
		return fmt.Errorf("seedance video provider task binding is invalid")
	}
	status := pending.UpstreamStatus
	if strings.TrimSpace(status) == "" {
		status = "queued"
	}
	if err := s.seedanceVideoTaskRepo.BindProviderTask(ctx, pending.StateID, pending.TaskID, status, time.Now().Add(seedanceVideoFirstPoll)); err != nil {
		return err
	}
	return nil
}

func (s *OpenAIGatewayService) LoadSeedanceVideoPendingBilling(ctx context.Context, taskID string, userID, apiKeyID int64) (*SeedanceVideoPendingBilling, error) {
	if s == nil || s.seedanceVideoTaskRepo == nil {
		return nil, fmt.Errorf("seedance video task repository is unavailable")
	}
	return s.seedanceVideoTaskRepo.GetByProviderTask(ctx, taskID, userID, apiKeyID)
}

func (s *OpenAIGatewayService) ClaimDueSeedanceVideoTasks(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*SeedanceVideoPendingBilling, error) {
	if s == nil || s.seedanceVideoTaskRepo == nil {
		return nil, fmt.Errorf("seedance video task repository is unavailable")
	}
	return s.seedanceVideoTaskRepo.ClaimDue(ctx, now, lease, limit)
}

func (s *OpenAIGatewayService) RescheduleSeedanceVideoTask(ctx context.Context, pending *SeedanceVideoPendingBilling, dueAt time.Time) error {
	return s.RescheduleSeedanceVideoTaskWithError(ctx, pending, dueAt, "")
}

func (s *OpenAIGatewayService) RescheduleSeedanceVideoTaskWithError(ctx context.Context, pending *SeedanceVideoPendingBilling, dueAt time.Time, lastError string) error {
	if s == nil || s.seedanceVideoTaskRepo == nil || pending == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	if leaseUntil, ok := seedanceVideoPendingLease(pending); ok {
		if leaseRepo, supported := s.seedanceVideoTaskRepo.(SeedanceVideoTaskRescheduleLeaseRepository); supported {
			return leaseRepo.RescheduleWithLease(ctx, pending.StateID, pending.UpstreamStatus, dueAt, lastError, leaseUntil)
		}
		return ErrSeedanceVideoLeaseMutationUnsupported
	}
	return s.seedanceVideoTaskRepo.Reschedule(ctx, pending.StateID, pending.UpstreamStatus, dueAt, lastError)
}

func (s *OpenAIGatewayService) CompleteSeedanceVideoTask(ctx context.Context, pending *SeedanceVideoPendingBilling, actualCost float64) error {
	if s == nil || s.seedanceVideoTaskRepo == nil || pending == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	if leaseUntil, ok := seedanceVideoPendingLease(pending); ok {
		if leaseRepo, supported := s.seedanceVideoTaskRepo.(SeedanceVideoTaskSettlementLeaseRepository); supported {
			return leaseRepo.MarkSettledWithLease(ctx, pending.StateID, actualCost, leaseUntil)
		}
		return ErrSeedanceVideoLeaseMutationUnsupported
	}
	return s.seedanceVideoTaskRepo.MarkSettled(ctx, pending.StateID, actualCost)
}

func (s *OpenAIGatewayService) ReleaseSeedanceVideoTask(ctx context.Context, pending *SeedanceVideoPendingBilling) error {
	status := ""
	if pending != nil {
		status = pending.UpstreamStatus
	}
	return s.ReleaseSeedanceVideoTaskWithStatus(ctx, pending, status)
}

// ReleaseSeedanceVideoTaskWithStatus closes the accounting lifecycle and, when
// supported by the repository, records the terminal provider status in the
// same mutation. This prevents a canceled/failed task from being reported as
// completed by a later provider poll.
func (s *OpenAIGatewayService) ReleaseSeedanceVideoTaskWithStatus(ctx context.Context, pending *SeedanceVideoPendingBilling, upstreamStatus string) error {
	if s == nil || s.seedanceVideoTaskRepo == nil || pending == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	upstreamStatus = strings.ToLower(strings.TrimSpace(upstreamStatus))
	if upstreamStatus == "" {
		upstreamStatus = strings.ToLower(strings.TrimSpace(pending.UpstreamStatus))
	}
	if leaseUntil, ok := seedanceVideoPendingLease(pending); ok {
		if upstreamStatus != "" {
			if leaseRepo, supported := s.seedanceVideoTaskRepo.(SeedanceVideoTaskReleaseStatusLeaseRepository); supported {
				return leaseRepo.MarkReleasedWithStatusWithLease(ctx, pending.StateID, upstreamStatus, leaseUntil)
			}
		}
		if leaseRepo, supported := s.seedanceVideoTaskRepo.(SeedanceVideoTaskReleaseLeaseRepository); supported {
			return leaseRepo.MarkReleasedWithLease(ctx, pending.StateID, leaseUntil)
		}
		return ErrSeedanceVideoLeaseMutationUnsupported
	}
	if releaseRepo, ok := s.seedanceVideoTaskRepo.(SeedanceVideoTaskReleaseStatusRepository); ok && upstreamStatus != "" {
		return releaseRepo.MarkReleasedWithStatus(ctx, pending.StateID, upstreamStatus)
	}
	return s.seedanceVideoTaskRepo.MarkReleased(ctx, pending.StateID)
}

// NormalizeSeedanceVideoReleaseStatus converts any provider terminal failure
// spelling to the two durable release states exposed by the gateway. Unknown
// values fail closed to failed; callers must never persist a queued/running
// value as a release intent.
func NormalizeSeedanceVideoReleaseStatus(status string) string {
	if NormalizeSeedanceVideoStatus(status) == SeedanceVideoResponseStatusCanceled {
		return SeedanceVideoResponseStatusCanceled
	}
	return SeedanceVideoResponseStatusFailed
}

// IsSeedanceVideoReleaseIntentStatus reports whether a durable upstream status
// means that billing must be released and the provider must no longer be
// polled for a possible completion.
func IsSeedanceVideoReleaseIntentStatus(status string) bool {
	switch NormalizeSeedanceVideoStatus(status) {
	case SeedanceVideoResponseStatusFailed, SeedanceVideoResponseStatusCanceled:
		return true
	default:
		return false
	}
}

// PersistSeedanceVideoReleaseIntent records the terminal provider outcome while
// keeping the accounting row in processing. The caller must still own an
// active lease; an unguarded fallback would allow an expired worker to overwrite
// a newer claim and is therefore rejected.
func (s *OpenAIGatewayService) PersistSeedanceVideoReleaseIntent(ctx context.Context, pending *SeedanceVideoPendingBilling, terminalStatus string) error {
	if s == nil || s.seedanceVideoTaskRepo == nil || pending == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	leaseUntil, ok := seedanceVideoPendingLease(pending)
	if !ok {
		return ErrSeedanceVideoLeaseMutationUnsupported
	}
	terminalStatus = NormalizeSeedanceVideoReleaseStatus(terminalStatus)
	// Keep the in-memory snapshot aligned even when the owner-checked write
	// fails. Recovery's error path uses this local intent to avoid rescheduling
	// the row back to a billable pending state.
	pending.UpstreamStatus = terminalStatus
	repo, supported := s.seedanceVideoTaskRepo.(SeedanceVideoTaskReleaseIntentLeaseRepository)
	if !supported {
		return ErrSeedanceVideoLeaseMutationUnsupported
	}
	return repo.MarkReleaseIntentWithLease(ctx, pending.StateID, terminalStatus, leaseUntil)
}

func seedanceVideoPendingLease(pending *SeedanceVideoPendingBilling) (time.Time, bool) {
	if pending == nil || pending.LeaseUntil == nil || pending.LeaseUntil.IsZero() {
		return time.Time{}, false
	}
	return *pending.LeaseUntil, true
}

func (s *OpenAIGatewayService) ClaimSeedanceVideoBilling(ctx context.Context, taskID string, userID, apiKeyID int64) (bool, error) {
	if s == nil || s.seedanceVideoTaskRepo == nil {
		return false, fmt.Errorf("seedance video task repository is unavailable")
	}
	return s.seedanceVideoTaskRepo.ClaimSettlement(ctx, taskID, userID, apiKeyID, seedanceVideoTaskLease)
}

// ClaimSeedanceVideoCancellation serializes cancellation with completion
// settlement. A fresh cancellation may claim a pending row, while a retry may
// reclaim only an expired row that already carries cancel_requested. An
// ordinary processing lease is never stolen by DELETE, even after expiry.
func (s *OpenAIGatewayService) ClaimSeedanceVideoCancellation(ctx context.Context, taskID string, userID, apiKeyID int64) (bool, error) {
	if s == nil || s.seedanceVideoTaskRepo == nil {
		return false, fmt.Errorf("seedance video task repository is unavailable")
	}
	repo, ok := s.seedanceVideoTaskRepo.(SeedanceVideoTaskCancellationRepository)
	if !ok {
		return false, fmt.Errorf("seedance video cancellation repository is unavailable")
	}
	return repo.ClaimCancellation(ctx, taskID, userID, apiKeyID, seedanceVideoTaskLease)
}

func (s *OpenAIGatewayService) ReleaseSeedanceVideoBilling(ctx context.Context, taskID string, userID, apiKeyID int64) error {
	pending, err := s.LoadSeedanceVideoPendingBilling(ctx, taskID, userID, apiKeyID)
	if err != nil {
		return err
	}
	return s.RescheduleSeedanceVideoTaskWithError(ctx, pending, time.Now().Add(seedanceVideoRetryDelay), "settlement retry")
}

// RetrySeedanceVideoBillingWithLease reschedules a claimed settlement using
// the exact owner token captured by the caller. It deliberately refuses to
// reload by task ID: a delayed billing callback must never mutate a newer
// worker's lease after the original claim expires.
func (s *OpenAIGatewayService) RetrySeedanceVideoBillingWithLease(ctx context.Context, pending *SeedanceVideoPendingBilling, lastError string) error {
	if s == nil || s.seedanceVideoTaskRepo == nil || pending == nil {
		return fmt.Errorf("seedance video task repository is unavailable")
	}
	if _, ok := seedanceVideoPendingLease(pending); !ok {
		return ErrSeedanceVideoLeaseMutationUnsupported
	}
	return s.RescheduleSeedanceVideoTaskWithError(
		ctx, pending, time.Now().Add(seedanceVideoRetryDelay), lastError,
	)
}

func StableSeedanceVideoBillingRequestID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	if strings.HasPrefix(taskID, "seedance-video:") {
		return taskID
	}
	return "seedance-video:" + taskID
}

type SeedanceVideoPricingSnapshot struct {
	TotalCostPerSecond  float64
	ActualCostPerSecond float64
	RateMultiplier      float64
	HoldAmount          float64
}

func seedanceVideoPricingBillingSeconds(info SeedanceVideoRequestInfo) int {
	billingSeconds := info.DurationSeconds
	if info.ReferenceVideoCount > 0 && strings.EqualFold(info.Model, videoprovider.ModelSeedance25) {
		if spec, ok := videoprovider.LookupModel(videoprovider.ModelSeedance25); ok {
			billingSeconds += spec.MaxReferenceVideoSeconds
		}
	}
	return billingSeconds
}

func (s *OpenAIGatewayService) ResolveSeedanceVideoPricingSnapshot(ctx context.Context, apiKey *APIKey, info SeedanceVideoRequestInfo) (*SeedanceVideoPricingSnapshot, error) {
	if s == nil || s.billingService == nil || apiKey == nil || apiKey.User == nil || apiKey.Group == nil || info.DurationSeconds <= 0 {
		return nil, fmt.Errorf("seedance video pricing dependencies are unavailable")
	}
	baseMultiplier := 1.0
	if s.cfg != nil {
		baseMultiplier = s.cfg.Default.RateMultiplier
	}
	if apiKey.GroupID != nil {
		baseMultiplier = s.ResolveUserGroupRateMultiplier(ctx, apiKey.User.ID, *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	rateMultiplier := resolveVideoRateMultiplier(apiKey, baseMultiplier)
	billingSeconds := seedanceVideoPricingBillingSeconds(info)
	result := &OpenAIForwardResult{
		Model: info.Model, BillingModel: info.Model, VideoProvider: PlatformSeedance,
		VideoCount: 1, VideoResolution: info.Resolution,
		VideoDurationSeconds: info.DurationSeconds, VideoBillingDurationSeconds: billingSeconds,
	}
	cost := s.calculateOpenAIVideoCost(ctx, info.Model, apiKey, result, rateMultiplier)
	if cost == nil || cost.TotalCost < 0 || cost.ActualCost < 0 {
		return nil, fmt.Errorf("seedance video pricing could not be resolved")
	}
	duration := float64(billingSeconds)
	holdAmount := QuantizeUsageBillingAmount(cost.ActualCost)
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		holdAmount = 0
	}
	return &SeedanceVideoPricingSnapshot{
		TotalCostPerSecond:  cost.TotalCost / duration,
		ActualCostPerSecond: cost.ActualCost / duration,
		RateMultiplier:      rateMultiplier,
		HoldAmount:          holdAmount,
	}, nil
}

func NewSeedanceVideoHoldID() string {
	return "seedance:" + generateRequestID()
}

func NewSeedanceVideoStateID() string {
	return "seedance-state:" + generateRequestID()
}

func (s *OpenAIGatewayService) ReserveSeedanceVideoBalance(ctx context.Context, userID, apiKeyID int64, holdID string, amount float64, payloadHash string) error {
	amount = QuantizeUsageBillingAmount(amount)
	if amount <= 0 {
		return nil
	}
	if s == nil || s.usageBillingRepo == nil || userID <= 0 || apiKeyID <= 0 || strings.TrimSpace(holdID) == "" {
		return errors.New("seedance video balance hold is unavailable")
	}
	_, err := s.usageBillingRepo.ReserveBatchImageBalance(ctx, &BatchImageBalanceHoldCommand{
		RequestID: BatchImageHoldRequestID(holdID), APIKeyID: apiKeyID,
		UserID: userID, BatchID: holdID, HoldAmount: amount, RequestPayloadHash: payloadHash,
	})
	if err == nil && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, userID)
	}
	return err
}

func (s *OpenAIGatewayService) ReleaseSeedanceVideoBalance(ctx context.Context, pending *SeedanceVideoPendingBilling) error {
	if pending == nil || pending.HoldAmount <= 0 || strings.TrimSpace(pending.HoldID) == "" {
		return nil
	}
	if s == nil || s.usageBillingRepo == nil {
		return errors.New("seedance video balance hold is unavailable")
	}
	_, err := s.usageBillingRepo.ReleaseBatchImageBalance(ctx, &BatchImageBalanceHoldCommand{
		RequestID: BatchImageReleaseRequestID(pending.HoldID), APIKeyID: pending.APIKeyID,
		UserID: pending.UserID, BatchID: pending.HoldID, HoldAmount: pending.HoldAmount,
		RequestPayloadHash: pending.RequestPayloadHash,
	})
	if errors.Is(err, ErrUsageBillingRequestConflict) {
		err = nil
	}
	if err == nil && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, pending.UserID)
	}
	return err
}

func IsSeedanceVideoPendingStatus(status string) bool {
	return videoprovider.IsPending(status)
}

func IsSeedanceVideoFailedStatus(status string) bool {
	return videoprovider.IsFailed(status)
}

func BuildSeedanceVideoCompletionBilling(pending *SeedanceVideoPendingBilling, status *OpenAIForwardResult) (*OpenAIForwardResult, *CostBreakdown, error) {
	if pending == nil || status == nil || status.VideoCount <= 0 {
		return nil, nil, fmt.Errorf("seedance video completion is not billable")
	}
	merged := *status
	merged.VideoProvider = PlatformSeedance
	merged.ResponseID = pending.TaskID
	merged.RequestID = StableSeedanceVideoBillingRequestID(pending.TaskID)
	merged.VideoCount = 1
	merged.ImageCount = 0
	if strings.TrimSpace(merged.Model) == "" {
		merged.Model = pending.Model
	}
	if strings.TrimSpace(merged.BillingModel) == "" {
		merged.BillingModel = pending.Model
	}
	driver, err := videoprovider.Resolve(pending.ProviderID)
	if err != nil {
		return nil, nil, err
	}
	if canonical, ok := videoprovider.CanonicalModel(merged.BillingModel); ok &&
		driver.BillingPolicy() == videoprovider.BillingOutputAndReference &&
		canonical == videoprovider.ModelSeedance25 && pending.ReferenceVideoCount > 0 &&
		merged.VideoReferenceInputSeconds <= 0 {
		return nil, nil, fmt.Errorf("seedance 2.5 reference video input duration is unavailable")
	}
	merged.VideoResolution = pending.Resolution
	if merged.VideoDurationSeconds <= 0 {
		merged.VideoDurationSeconds = pending.DurationSeconds
	}
	merged.VideoBillingDurationSeconds = merged.VideoDurationSeconds
	if driver.BillingPolicy() == videoprovider.BillingOutputAndReference {
		merged.VideoBillingDurationSeconds += max(0, merged.VideoReferenceInputSeconds)
	}
	if merged.VideoBillingDurationSeconds <= 0 {
		return nil, nil, fmt.Errorf("seedance video completion duration is invalid")
	}
	if e2e := GrokVideoE2EDuration(pending.CreatedAt, time.Now()); e2e > 0 {
		merged.Duration = e2e
	}
	seconds := float64(merged.VideoBillingDurationSeconds)
	cost := &CostBreakdown{
		TotalCost:   pending.TotalCostPerSecond * seconds,
		ActualCost:  pending.ActualCostPerSecond * seconds,
		BillingMode: string(BillingModeVideo),
	}
	return &merged, cost, nil
}

func (s *OpenAIGatewayService) ForwardSeedanceVideo(ctx context.Context, c *gin.Context, account *Account, providerID string, endpoint SeedanceVideoEndpoint, taskID string, createRequest *SeedanceVideoCreateRequest) (*SeedanceVideoForwardResult, error) {
	startedAt := time.Now()
	if account == nil || !account.IsSeedance() || account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("seedance API key account is required")
	}
	apiKey := account.GetSeedanceAPIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("seedance account API key is missing")
	}
	if strings.TrimSpace(providerID) == "" {
		providerID = string(account.GetVideoProviderID())
	}
	driver, err := videoprovider.Resolve(providerID)
	if err != nil {
		return nil, err
	}
	if endpoint == SeedanceVideoEndpointContent {
		return s.forwardSeedanceVideoContent(ctx, c, account, driver, apiKey, taskID, startedAt)
	}
	operation, err := seedanceVideoOperation(endpoint)
	if err != nil {
		return nil, err
	}
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	req, err := driver.BuildRequest(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), videoprovider.RequestParams{
		BaseURL: account.GetVideoProviderBaseURL(string(driver.ID())), APIKey: apiKey,
		Operation: operation, TaskID: taskID, CreateRequest: createRequest,
	})
	if err != nil {
		return nil, err
	}
	account.ApplyHeaderOverrides(req.Header)
	resp, respBody, err := s.doSeedanceJSON(ctx, c, account, req)
	if err != nil {
		return nil, err
	}
	task, parseErr := driver.ParseTask(respBody, taskID)
	if parseErr != nil {
		if endpoint == SeedanceVideoEndpointCancel && len(bytes.TrimSpace(respBody)) == 0 {
			task = videoprovider.Task{ID: taskID, Status: videoprovider.StatusCanceled}
		} else {
			return nil, &UpstreamFailoverError{
				StatusCode: http.StatusBadGateway, ResponseBody: respBody, ResponseHeaders: resp.Header.Clone(),
				NextAccountAction: NextAccountRetry,
			}
		}
	}
	if endpoint == SeedanceVideoEndpointCreate {
		// The create endpoint is asynchronous. Some providers use "success" or
		// "succeeded" to acknowledge job creation, and their adapters normalize
		// those values to StatusCompleted for status polling. Do not expose that
		// acknowledgement as a finished, downloadable video; the next status
		// lookup will determine the real terminal state.
		if task.Status == videoprovider.StatusCompleted || strings.TrimSpace(string(task.Status)) == "" {
			task.Status = videoprovider.StatusPending
		}
	}
	if endpoint == SeedanceVideoEndpointCancel {
		// DELETE success is normally an acknowledgement rather than a task
		// status. Preserve explicit terminal outcomes, however: a provider may
		// report that cancellation failed or that the task completed just before
		// the DELETE reached it. Generic success/empty/non-terminal responses
		// retain the gateway's cancellation acknowledgement semantics.
		task.Status = seedanceVideoCancellationTaskStatus(respBody, task)
	}
	result := seedanceForwardResultFromTask(task)
	taskStatus := string(task.Status)
	if endpoint == SeedanceVideoEndpointCreate && strings.TrimSpace(result.ResponseID) == "" {
		return nil, &UpstreamFailoverError{
			StatusCode: http.StatusBadGateway, ResponseBody: respBody, ResponseHeaders: resp.Header.Clone(),
			NextAccountAction: NextAccountRetry,
		}
	}
	result.RequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	result.ResponseHeaders = resp.Header.Clone()
	result.Duration = time.Since(startedAt)
	result.VideoProvider = PlatformSeedance
	forwardResult := &SeedanceVideoForwardResult{OpenAIForwardResult: result, TaskStatus: taskStatus}
	// JSON responses are rendered by the handler after task ownership and
	// billing state have been persisted. Keeping this service method free of
	// response writes prevents status/cancel from being written twice and
	// prevents provider-specific response bodies from leaking to clients.
	return forwardResult, nil
}

// seedanceVideoCancellationTaskStatus applies the cancellation endpoint's
// response semantics without forwarding provider-specific status values. The
// provider adapters normalize "success" and "completed" to the same internal
// status, so inspect the structured raw status once more to distinguish a
// generic acknowledgement from an explicit completed task.
func seedanceVideoCancellationTaskStatus(body []byte, parsed videoprovider.Task) videoprovider.Status {
	rawStatus := seedanceVideoRawTaskStatus(body)
	switch rawStatus {
	case "failed", "error":
		return videoprovider.StatusFailed
	case "completed":
		return videoprovider.StatusCompleted
	case "canceled", "cancelled":
		return videoprovider.StatusCanceled
	case "success", "succeeded", "":
		// Some providers return a 2xx envelope such as {"success":false}
		// without a task status. Treat an explicit boolean failure as a failed
		// cancellation; an empty body or a positive acknowledgement remains
		// idempotent cancellation success.
		if success, present := seedanceVideoRawBoolean(body, "success", "data.success"); present && !success {
			return videoprovider.StatusFailed
		}
		return videoprovider.StatusCanceled
	default:
		// A parser-level failure is still an explicit terminal failure even if
		// a provider used an unrecognized casing/alias in its payload.
		if success, present := seedanceVideoRawBoolean(body, "success", "data.success"); present && !success {
			return videoprovider.StatusFailed
		}
		if parsed.Status == videoprovider.StatusFailed {
			return videoprovider.StatusFailed
		}
		return videoprovider.StatusCanceled
	}
}

func seedanceVideoRawBoolean(body []byte, paths ...string) (bool, bool) {
	var payload map[string]any
	if len(bytes.TrimSpace(body)) == 0 || json.Unmarshal(body, &payload) != nil || payload == nil {
		return false, false
	}
	for _, path := range paths {
		value := any(payload)
		for _, part := range strings.Split(path, ".") {
			object, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value = object[part]
		}
		if success, ok := value.(bool); ok {
			return success, true
		}
	}
	return false, false
}

func seedanceVideoRawTaskStatus(body []byte) string {
	var payload map[string]any
	if len(bytes.TrimSpace(body)) == 0 || json.Unmarshal(body, &payload) != nil || payload == nil {
		return ""
	}
	for _, path := range []string{"status", "data.status"} {
		value := any(payload)
		for _, part := range strings.Split(path, ".") {
			object, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value = object[part]
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.ToLower(strings.TrimSpace(text))
		}
	}
	return ""
}

func (s *OpenAIGatewayService) FetchSeedanceVideoStatus(ctx context.Context, account *Account, providerID, taskID string) (*SeedanceVideoForwardResult, error) {
	return s.ForwardSeedanceVideo(ctx, nil, account, providerID, SeedanceVideoEndpointStatus, taskID, nil)
}

// CommitSeedanceVideoResponse is retained for callers compiled against the
// original service API. New handlers should use their canonical response
// writer directly; this compatibility method emits the same provider-neutral
// envelope and never forwards the upstream JSON body.
//
// Deprecated: use BuildSeedanceVideoResponse and the HTTP handler writer.
func (s *OpenAIGatewayService) CommitSeedanceVideoResponse(c *gin.Context, result *SeedanceVideoForwardResult) error {
	if c == nil || c.Writer == nil || result == nil || result.OpenAIForwardResult == nil {
		return fmt.Errorf("seedance video response is invalid")
	}
	if c.Writer.Written() || IsResponseCommitted(c) {
		return fmt.Errorf("seedance video response is already committed")
	}
	forward := result.OpenAIForwardResult
	response := BuildSeedanceVideoResponse(result, SeedanceVideoResponseMeta{
		ID:         forward.ResponseID,
		Model:      forward.Model,
		Resolution: forward.VideoResolution,
		Duration:   forward.VideoDurationSeconds,
		// This compatibility writer has no durable settlement snapshot. Keep a
		// provider completion non-terminal rather than returning an unusable
		// completed response without a content URL.
		SettlementStatus: SeedanceVideoSettlementPending,
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, response)
	MarkResponseCommitted(c)
	return nil
}

func (s *OpenAIGatewayService) forwardSeedanceVideoContent(ctx context.Context, c *gin.Context, account *Account, driver videoprovider.Driver, apiKey, taskID string, startedAt time.Time) (*SeedanceVideoForwardResult, error) {
	baseURL := account.GetVideoProviderBaseURL(string(driver.ID()))
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	statusReq, err := driver.BuildRequest(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), videoprovider.RequestParams{
		BaseURL: baseURL, APIKey: apiKey, Operation: videoprovider.OperationStatus, TaskID: taskID,
	})
	if err != nil {
		return nil, err
	}
	account.ApplyHeaderOverrides(statusReq.Header)
	statusResp, statusBody, err := s.doSeedanceJSON(ctx, c, account, statusReq)
	if err != nil {
		return nil, err
	}
	task, err := driver.ParseTask(statusBody, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != videoprovider.StatusCompleted {
		return nil, ErrSeedanceVideoContentNotReady
	}

	contentReq, err := driver.BuildRequest(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), videoprovider.RequestParams{
		BaseURL: baseURL, APIKey: apiKey, Operation: videoprovider.OperationContent, TaskID: taskID,
	})
	if err != nil {
		return nil, err
	}
	if c != nil {
		if value := strings.TrimSpace(c.GetHeader("Range")); value != "" {
			contentReq.Header.Set("Range", value)
		}
		if value := strings.TrimSpace(c.GetHeader("If-Range")); value != "" {
			contentReq.Header.Set("If-Range", value)
		}
	}
	account.ApplyHeaderOverrides(contentReq.Header)
	proxyURL := accountProxyURL(account)
	upstreamStart := time.Now()
	contentResp, err := s.httpUpstream.Do(contentReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = contentResp.Body.Close() }()
	if contentResp.StatusCode >= 400 && contentResp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		body, readErr := ReadUpstreamResponseBody(contentResp.Body, s.cfg, c, seedanceTooLargeErrorWriter(c))
		if readErr != nil {
			// ReadUpstreamResponseBody writes the formatted 502 response for an
			// oversized body. Mark it here so the handler does not append a second
			// JSON error while unwinding the forwarding path.
			if c != nil && c.Writer != nil && c.Writer.Written() {
				MarkResponseCommitted(c)
			}
			return nil, readErr
		}
		return nil, seedanceFailoverError(contentResp, body)
	}
	if err := writeGrokMediaContentResponse(c, contentResp); err != nil {
		return nil, err
	}
	result := seedanceForwardResultFromTask(task)
	result.RequestID = firstNonEmpty(contentResp.Header.Get("x-request-id"), statusResp.Header.Get("x-request-id"))
	result.ResponseHeaders = contentResp.Header.Clone()
	result.Duration = time.Since(startedAt)
	result.VideoProvider = PlatformSeedance
	return &SeedanceVideoForwardResult{OpenAIForwardResult: result, TaskStatus: string(task.Status)}, nil
}

func (s *OpenAIGatewayService) doSeedanceJSON(ctx context.Context, c *gin.Context, account *Account, req *http.Request) (*http.Response, []byte, error) {
	proxyURL := accountProxyURL(account)
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, seedanceTooLargeErrorWriter(c))
	if err != nil {
		// The too-large callback has already written the client-facing error.
		// Preserve that fact for the outer handler's fallback error path.
		if c != nil && c.Writer != nil && c.Writer.Written() {
			MarkResponseCommitted(c)
		}
		return nil, nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, nil, seedanceFailoverError(resp, body)
	}
	return resp, body, nil
}

// seedanceTooLargeErrorWriter only writes a client-facing response when the
// forwarding call belongs to an HTTP request. Recovery polling deliberately
// passes a nil Gin context, so invoking the normal JSON writer there would
// panic while the worker is merely trying to classify the upstream error.
func seedanceTooLargeErrorWriter(c *gin.Context) TooLargeWriter {
	if c == nil {
		return nil
	}
	return openAITooLargeError
}

func accountProxyURL(account *Account) string {
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

func seedanceVideoOperation(endpoint SeedanceVideoEndpoint) (videoprovider.Operation, error) {
	switch endpoint {
	case SeedanceVideoEndpointCreate:
		return videoprovider.OperationCreate, nil
	case SeedanceVideoEndpointStatus:
		return videoprovider.OperationStatus, nil
	case SeedanceVideoEndpointContent:
		return videoprovider.OperationContent, nil
	case SeedanceVideoEndpointCancel:
		return videoprovider.OperationCancel, nil
	default:
		return "", fmt.Errorf("unsupported seedance video endpoint %q", endpoint)
	}
}

func seedanceFailoverError(resp *http.Response, body []byte) *UpstreamFailoverError {
	err := &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body, ResponseHeaders: resp.Header.Clone()}
	if (resp.StatusCode >= 300 && resp.StatusCode < 400) ||
		resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusRequestEntityTooLarge ||
		resp.StatusCode == http.StatusUnprocessableEntity {
		err.NextAccountAction = NextAccountStop
	}
	return err
}

func seedanceForwardResultFromTask(task videoprovider.Task) *OpenAIForwardResult {
	result := &OpenAIForwardResult{
		ResponseID: task.ID, Model: task.Model, BillingModel: task.Model,
		VideoResolution: task.Resolution, VideoDurationSeconds: task.DurationSeconds,
		VideoReferenceInputSeconds: task.ReferenceInputSeconds,
	}
	if task.Status == videoprovider.StatusCompleted {
		result.VideoCount = 1
	}
	return result
}
