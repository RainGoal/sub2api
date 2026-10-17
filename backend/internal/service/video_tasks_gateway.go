package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

// VideoSubmissionUnknownError means an upstream attempt may have created a job.
// Callers must retain the reservation and never automatically submit again.
type VideoSubmissionUnknownError struct{ Cause error }

var ErrVideoTaskSalesCapabilityMismatch = errors.New("video account cannot satisfy the frozen sales contract")

func (e *VideoSubmissionUnknownError) Error() string { return "video submission outcome is unknown" }
func (e *VideoSubmissionUnknownError) Unwrap() error { return e.Cause }

// PrepareVideoRequestForKey only considers capabilities from explicitly enabled
// accounts in this key's group. Discovery never grants a model permission.
func (s *OpenAIGatewayService) PrepareVideoRequestForKey(ctx context.Context, key *APIKey, body []byte) (*SeedanceVideoCreateRequest, []byte, SeedanceVideoRequestInfo, error) {
	if !s.VideoTasksEnabled() {
		return PrepareSeedanceVideoRequest(body)
	}
	var input struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		return nil, nil, SeedanceVideoRequestInfo{}, fmt.Errorf("request body must be valid JSON")
	}
	if key == nil || key.GroupID == nil || s.accountRepo == nil {
		return PrepareSeedanceVideoRequest(body)
	}
	accounts, err := s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *key.GroupID, PlatformSeedance)
	if err != nil {
		return nil, nil, SeedanceVideoRequestInfo{}, err
	}
	var capabilityError error
	legacyAvailable := false
	for i := range accounts {
		account := &accounts[i]
		if !account.isSeedanceModelSupported(input.Model) {
			continue
		}
		if account.GetVideoProviderID() != videoprovider.ProviderVideoTasksJSONV1 {
			legacyAvailable = true
			continue
		}
		capability, ok := account.VideoModelCapability(input.Model)
		if !ok {
			continue
		}
		request, info, err := videoprovider.ParseCreateRequestWithCapabilities(body, []videoprovider.VideoModelCapability{capability})
		if err != nil {
			capabilityError = err
			continue
		}
		if _, err := s.ResolveSeedanceVideoPricingSnapshot(ctx, key, info); err != nil {
			capabilityError = err
			continue
		}
		normalized, err := request.CanonicalJSON()
		return request, normalized, info, err
	}
	if legacyAvailable {
		return PrepareSeedanceVideoRequest(body)
	}
	if capabilityError != nil {
		return nil, nil, SeedanceVideoRequestInfo{}, capabilityError
	}
	return nil, nil, SeedanceVideoRequestInfo{}, fmt.Errorf("video model is not configured for this key")
}

// Compute the reservation using the selected capability and frozen prices.
// An explicit sales limit is shared by all accounts; otherwise the account's
// finite output/reference bounds must fit the reservation already held.
func videoTaskSalesHoldForCapability(snapshot *VideoTaskSalesSnapshot, capability videoprovider.VideoModelCapability, duration, references int) (float64, error) {
	if err := snapshot.Validate(); err != nil {
		return 0, err
	}
	if snapshot.BillingUnit == VideoBillingPerSecond && snapshot.MaxBillableSeconds > 0 && float64(duration) > snapshot.MaxBillableSeconds {
		return 0, fmt.Errorf("video billing duration exceeds its configured limit")
	}
	output, reference := float64(duration), float64(0)
	if snapshot.BillingUnit == VideoBillingPerSecond && snapshot.QuantityPolicy != VideoQuantityRequestedSeconds {
		if !capability.MeteringTrusted {
			return 0, fmt.Errorf("trusted video metering is required for actual-second pricing")
		}
		if snapshot.MaxBillableSeconds > 0 {
			output = snapshot.MaxBillableSeconds
		} else {
			if capability.MaxDuration <= 0 {
				return 0, fmt.Errorf("video output duration has no finite sales limit")
			}
			output = float64(capability.MaxDuration)
			if snapshot.QuantityPolicy == VideoQuantityOutputAndReferenceSeconds && references > 0 {
				if capability.MaxReferenceVideoSeconds <= 0 {
					return 0, fmt.Errorf("reference video duration has no finite sales limit")
				}
				reference = float64(capability.MaxReferenceVideoSeconds)
			}
		}
	}
	return snapshot.HoldAmount(output, reference)
}

func (s *OpenAIGatewayService) resolveVideoTasksPricingSnapshot(ctx context.Context, key *APIKey, info SeedanceVideoRequestInfo, multiplier float64) (*SeedanceVideoPricingSnapshot, error) {
	price, err := ResolveVideoSalesPrice(ctx, s.channelService, key.Group, info.Model, info.Resolution)
	if err != nil {
		return nil, err
	}
	if price == nil || (info.Capability != nil && price.Version != VideoPricingVersion) {
		return nil, fmt.Errorf("explicit video sales pricing is required")
	}
	if price.BillingUnit == VideoBillingPerSecond && price.MaxBillableSeconds > 0 && float64(info.DurationSeconds) > price.MaxBillableSeconds {
		return nil, fmt.Errorf("video billing duration exceeds its configured limit")
	}
	if info.Capability == nil && price.Version == 0 {
		if info.DurationSeconds <= 0 {
			return nil, fmt.Errorf("video billing duration is invalid")
		}
		actualUnitPrice := price.UnitPrice * multiplier
		hold := actualUnitPrice * float64(seedanceVideoPricingBillingSeconds(info))
		if !validSeedanceAccountCost(actualUnitPrice) || !validSeedanceAccountCost(hold) {
			return nil, fmt.Errorf("video sales amount exceeds the supported range")
		}
		hold = QuantizeUsageBillingAmount(hold)
		if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
			hold = 0
		}
		return &SeedanceVideoPricingSnapshot{TotalCostPerSecond: price.UnitPrice,
			ActualCostPerSecond: actualUnitPrice, RateMultiplier: multiplier, HoldAmount: hold}, nil
	}
	defaultPolicy := VideoQuantityOutputSeconds
	if info.ReferenceVideoCount > 0 {
		defaultPolicy = VideoQuantityOutputAndReferenceSeconds
	}
	snapshot, err := NewVideoTaskSalesSnapshot(price, multiplier, defaultPolicy, 0)
	if err != nil {
		return nil, err
	}
	output, reference := float64(info.DurationSeconds), float64(0)
	if snapshot.BillingUnit == VideoBillingPerSecond && snapshot.QuantityPolicy != VideoQuantityRequestedSeconds {
		if snapshot.MaxBillableSeconds > 0 {
			output = snapshot.MaxBillableSeconds
		} else if info.Capability != nil {
			output = float64(info.Capability.MaxDuration)
			if snapshot.QuantityPolicy == VideoQuantityOutputAndReferenceSeconds && info.ReferenceVideoCount > 0 {
				reference = float64(info.Capability.MaxReferenceVideoSeconds)
			}
		} else {
			output = float64(seedanceVideoPricingBillingSeconds(info))
		}
	}
	var hold float64
	if info.Capability != nil {
		hold, err = videoTaskSalesHoldForCapability(snapshot, *info.Capability, info.DurationSeconds, info.ReferenceVideoCount)
	} else {
		hold, err = snapshot.HoldAmount(output, reference)
	}
	if err != nil {
		return nil, err
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		hold = 0
	}
	return &SeedanceVideoPricingSnapshot{SalesSnapshot: snapshot, RateMultiplier: multiplier, HoldAmount: hold}, nil
}

func buildVideoTasksCompletionBilling(pending *SeedanceVideoPendingBilling, result *OpenAIForwardResult) (*OpenAIForwardResult, *CostBreakdown, error) {
	output, reference := result.VideoDurationSecondsExact, result.VideoReferenceInputSecondsExact
	if pending.ExecutionSnapshot == nil || pending.ExecutionSnapshot.Protocol != string(videoprovider.ProviderVideoTasksJSONV1) {
		if output == 0 {
			output = float64(result.VideoDurationSeconds)
		}
		if reference == 0 {
			reference = float64(result.VideoReferenceInputSeconds)
		}
	}
	measure := func(unit VideoBillingUnit, policy VideoQuantityPolicy) (float64, error) {
		if unit == VideoBillingPerVideo {
			return 0, nil
		}
		if policy == VideoQuantityRequestedSeconds {
			return float64(pending.DurationSeconds), nil
		}
		if pending.ExecutionSnapshot != nil && pending.ExecutionSnapshot.Capability != nil && !pending.ExecutionSnapshot.Capability.MeteringTrusted {
			return 0, fmt.Errorf("video actual metering is not trusted")
		}
		if output <= 0 || (policy == VideoQuantityOutputAndReferenceSeconds && pending.ReferenceVideoCount > 0 && reference <= 0) {
			return 0, fmt.Errorf("video actual metering is unavailable")
		}
		return output, nil
	}
	salesOutput, err := measure(pending.SalesSnapshot.BillingUnit, pending.SalesSnapshot.QuantityPolicy)
	if err != nil {
		return nil, nil, err
	}
	cost, err := pending.SalesSnapshot.Cost(salesOutput, reference)
	if err != nil {
		return nil, nil, err
	}
	if pending.AccountCost != nil && pending.AccountCost.Version == VideoPricingVersion {
		purchaseOutput, err := measure(pending.AccountCost.BillingUnit, pending.AccountCost.QuantityPolicy)
		if err != nil {
			return nil, nil, err
		}
		if _, err := pending.AccountCost.Cost(purchaseOutput, reference); err != nil {
			return nil, nil, err
		}
	}
	if pending.HoldAmount > 0 && QuantizeUsageBillingAmount(cost.ActualCost) > pending.HoldAmount {
		return nil, nil, fmt.Errorf("video settlement exceeds its reserved amount")
	}
	result.VideoResolution = pending.Resolution
	result.VideoPricingVersion = VideoPricingVersion
	if result.VideoDurationSeconds == 0 {
		result.VideoDurationSeconds = pending.DurationSeconds
	}
	result.VideoBillingDurationSeconds = int(salesOutput)
	if e2e := GrokVideoE2EDuration(pending.CreatedAt, time.Now()); e2e > 0 {
		result.Duration = e2e
	}
	return result, cost, nil
}

// PrepareVideoRequestForAccount checks the selected account before any upstream
// request. A different provider may support a different subset of the model.
func (s *OpenAIGatewayService) PrepareVideoRequestForAccount(request *SeedanceVideoCreateRequest, account *Account) (*SeedanceVideoCreateRequest, error) {
	if request == nil || account == nil {
		return nil, fmt.Errorf("video request and account are required")
	}
	if account.GetVideoProviderID() != videoprovider.ProviderVideoTasksJSONV1 {
		// The request has already passed public-input validation. Preserve its
		// typed references and raw provider fields; the legacy driver performs
		// its own model and provider-specific checks before sending upstream.
		return request, nil
	}
	if !s.VideoTasksEnabled() {
		return nil, fmt.Errorf("video tasks JSON v1 is disabled")
	}
	capability, ok := account.VideoModelCapability(request.RequestedModel)
	if !ok || !account.isSeedanceModelSupported(request.RequestedModel) {
		return nil, fmt.Errorf("video model is not enabled on this account")
	}
	// Reparse public inputs with this account's rules, preserving the defaults
	// already selected by the initial capability validation.
	payload := map[string]any{}
	for key, value := range request.Raw {
		payload[key] = value
	}
	payload["model"], payload["duration"], payload["resolution"] = request.RequestedModel, request.Duration, request.Resolution
	delete(payload, "size")
	if capability.DurationMode == videoprovider.VideoDurationUnknown {
		if request.Duration != 0 {
			return nil, fmt.Errorf("video duration contract differs on this account")
		}
		delete(payload, "duration")
	}
	if !capability.ResolutionControl && strings.TrimSpace(request.Resolution) == "" {
		delete(payload, "resolution")
	}
	if request.AspectRatio != "" {
		payload["aspect_ratio"] = request.AspectRatio
		delete(payload, "ratio")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	validated, _, err := videoprovider.ParseCreateRequestWithCapabilities(body, []videoprovider.VideoModelCapability{capability})
	if err == nil && (validated.Duration != request.Duration || !strings.EqualFold(validated.Resolution, request.Resolution)) {
		return nil, fmt.Errorf("video parameters differ on this account")
	}
	return validated, err
}
