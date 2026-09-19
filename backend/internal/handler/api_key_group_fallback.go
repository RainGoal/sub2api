package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type apiKeyFallbackResolver interface {
	ResolveFallbackGroup(context.Context, *service.APIKey) (*service.Group, error)
}

type apiKeyFallbackBilling interface {
	CheckFallbackBillingEligibility(context.Context, *service.User, *service.APIKey, *service.Group, string) error
}

type apiKeyFallbackSecurityAuditor interface {
	checkSecurityAudit(*gin.Context, *zap.Logger, *service.APIKey, middleware2.AuthSubject, string, string, []byte) *securityaudit.Decision
}

// Group-scoped blocking scanners must run against the target even if the
// primary group was excluded by their configuration.
func checkAPIKeyFallbackSecurityAudit(auditor apiKeyFallbackSecurityAuditor, c *gin.Context, key *service.APIKey, model string, body []byte) error {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		return service.ErrGroupNotAllowed
	}
	protocol := service.ContentModerationProtocolOpenAIResponses
	if strings.HasSuffix(c.Request.URL.Path, "/messages") {
		protocol = service.ContentModerationProtocolAnthropicMessages
	} else if strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
		protocol = service.ContentModerationProtocolOpenAIChat
	}
	c.Set(securityAuditCompletedContextKey, false)
	decision := auditor.checkSecurityAudit(c, requestLogger(c, "handler.api_key_group_fallback"), key, subject, protocol, model, body)
	if decision != nil && !decision.AllowNextStage {
		return service.ErrGroupNotAllowed
	}
	return nil
}

// apiKeyGroupFallbackCause distinguishes capacity from policy/configuration
// failures even when a scheduler wraps both in ErrNoAvailableAccounts.
type apiKeyGroupFallbackCause struct {
	SelectionErr  error
	UpstreamErr   *service.UpstreamFailoverError
	ModelNotFound bool
	ProfitVetoed  bool
}

type apiKeyGroupFallback struct {
	resolver     apiKeyFallbackResolver
	billing      apiKeyFallbackBilling
	primaryID    int64
	maxSwitches  int
	primaryLimit int
	headers      http.Header
	enabled      bool
	used         bool
}

func captureAPIKeyFallbackBody(key *service.APIKey, body []byte) []byte {
	if key == nil || key.FallbackGroupID == nil {
		return nil
	}
	return append([]byte(nil), body...)
}

func newAPIKeyGroupFallback(c *gin.Context, resolver apiKeyFallbackResolver, billing apiKeyFallbackBilling, key *service.APIKey, body []byte, maxSwitches int) *apiKeyGroupFallback {
	f := &apiKeyGroupFallback{resolver: resolver, billing: billing, maxSwitches: maxSwitches, primaryLimit: maxSwitches}
	if resolver == nil || billing == nil || maxSwitches < 1 ||
		key == nil || key.GroupID == nil || key.Group == nil || key.User == nil || key.FallbackGroupID == nil ||
		(key.Group.Platform != service.PlatformOpenAI && key.Group.Platform != service.PlatformAnthropic) ||
		key.Group.SubscriptionType != service.SubscriptionTypeStandard ||
		key.Group.FallbackGroupID != nil || key.Group.FallbackGroupIDOnInvalidRequest != nil ||
		!apiKeyFallbackReplayableBody(body) {
		return f
	}
	f.enabled = true
	f.primaryID = *key.GroupID
	if c != nil && c.Request != nil {
		f.headers = c.Request.Header.Clone()
	}
	// Reserve approximately half of the existing switch budget for the target;
	// the transition itself consumes one switch. The total is never reset.
	f.primaryLimit = maxSwitches / 2
	return f
}

func (f *apiKeyGroupFallback) PrimarySwitchLimit(requests ...*gin.Context) int {
	// Shared-gateway callers pass c while reserving their primary account
	// budget so a forced-platform route retains its original retry allowance.
	for _, c := range requests {
		if c != nil && c.Request != nil {
			if platform, _ := c.Request.Context().Value(ctxkey.ForcePlatform).(string); platform != "" {
				f.enabled = false
			}
		}
	}
	if !f.enabled {
		return f.maxSwitches
	}
	return f.primaryLimit
}

// Try validates on an isolated context. prepare must only validate/remap the
// canonical request, never forward it or write a response. Nil key means the
// original failure should be returned; errors are diagnostic, not a new replay.
func (f *apiKeyGroupFallback) Try(c *gin.Context, current *service.APIKey, model string, switchCount *int, cause apiKeyGroupFallbackCause, prepare func(*gin.Context, *service.APIKey) error) (*service.APIKey, error) {
	if f == nil || !f.enabled || f.used || c == nil || c.Request == nil || c.Writer == nil ||
		current == nil || current.GroupID == nil || *current.GroupID != f.primaryID ||
		switchCount == nil || *switchCount >= f.maxSwitches || c.Request.Context().Err() != nil ||
		c.Writer.Written() || service.GetOpsCyberPolicy(c) != nil || c.Request.Method != http.MethodPost || !apiKeyFallbackHTTPPath(c.Request.URL.Path) ||
		!apiKeyFallbackCauseAllowed(current.Group, cause) {
		return nil, nil
	}
	if forcePlatform, _ := c.Request.Context().Value(ctxkey.ForcePlatform).(string); forcePlatform != "" {
		return nil, nil
	}
	// Even an invalid/revoked target is resolved at most once per request.
	f.used = true
	group, err := f.resolver.ResolveFallbackGroup(c.Request.Context(), current)
	if err != nil || group == nil {
		return nil, err
	}
	if !group.ModelAllowlist.Allows(model) || (group.ClaudeCodeOnly && !service.IsClaudeCodeClient(c.Request.Context())) {
		return nil, service.ErrGroupNotAllowed
	}
	key := cloneAPIKeyWithGroup(current, group)
	user := *current.User
	user.UserGroupRPMOverride = nil
	key.User = &user
	key.FallbackGroupID = nil
	trial := c.Copy()
	// WithContext retains the original cancellation/deadline; changing groups
	// never grants another timeout window or resets same-account retry counts.
	trial.Request = c.Request.Clone(service.WithAPIKeyFallbackGroup(c.Request.Context(), group))
	if f.headers != nil {
		// Forwarding policies may have removed or rewritten provider headers.
		// The destination derives its request from the original client headers.
		trial.Request.Header = f.headers.Clone()
	}
	trial.Set(string(middleware2.ContextKeyAPIKey), key)
	trial.Set(string(middleware2.ContextKeySubscription), (*service.UserSubscription)(nil))
	if prepare != nil {
		if err := prepare(trial, key); err != nil {
			return nil, err
		}
	}
	if err := f.billing.CheckFallbackBillingEligibility(trial.Request.Context(), key.User, key, group, group.Platform); err != nil {
		return nil, err
	}
	if c.Request.Context().Err() != nil || c.Writer.Written() {
		return nil, nil
	}
	*switchCount++
	c.Request = trial.Request
	for name, value := range trial.Keys {
		c.Set(name, value)
	}
	service.ClearOpsUpstreamModel(c)
	c.Set("api_key_fallback_from_group_id", f.primaryID)
	c.Set("api_key_fallback_to_group_id", group.ID)
	return key, nil
}

func apiKeyFallbackCauseAllowed(group *service.Group, cause apiKeyGroupFallbackCause) bool {
	if cause.ModelNotFound || cause.ProfitVetoed {
		return false
	}
	// An earlier upstream failure cannot turn a later local selection veto into
	// capacity. Gateway handlers can supply both errors after draining a pool.
	if cause.SelectionErr != nil && !apiKeyFallbackSelectionAllowed(group, cause.SelectionErr) {
		return false
	}
	if upstream := cause.UpstreamErr; upstream != nil {
		if !upstream.ShouldRetryNextAccount() || upstream.Scope == service.GatewayFailureScopeRequest {
			return false
		}
		return upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden ||
			upstream.StatusCode == http.StatusTooManyRequests || upstream.StatusCode >= 500
	}
	return cause.SelectionErr != nil
}

func apiKeyFallbackSelectionAllowed(group *service.Group, selectionErr error) bool {
	if !errors.Is(selectionErr, service.ErrNoAvailableAccounts) {
		return false
	}
	// Some schedulers omit per-filter counters. An empty pool under a profit
	// gate is ambiguous, so it must not be promoted to a capacity failure.
	if group != nil && group.ProfitControlEnabled {
		return false
	}
	message := strings.ToLower(selectionErr.Error())
	for _, policy := range []string{"channel pricing restriction", "profit_threshold=", "profit_invalid_account_rate=", "platform unknown", "policy", "permission"} {
		if strings.Contains(message, policy) && !strings.Contains(message, policy+"0") {
			return false
		}
	}
	return true
}

func apiKeyFallbackHTTPPath(path string) bool {
	return strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/chat/completions") ||
		strings.HasSuffix(path, "/responses")
}

func apiKeyFallbackReplayableBody(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	for _, field := range []string{"previous_response_id", "conversation", "background", "audio", "context_management"} {
		value := gjson.GetBytes(body, field)
		if value.Exists() && value.Type != gjson.Null && value.Type != gjson.False && value.String() != "" {
			return false
		}
	}
	model := gjson.GetBytes(body, "model").String()
	if service.IsGPTImageGenerationModel(model) || service.IsExplicitImageGenerationIntent("/v1/responses", model, body) {
		return false
	}
	for _, modality := range gjson.GetBytes(body, "modalities").Array() {
		if modality.String() != "text" {
			return false
		}
	}
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		// Hosted tools can execute work before returning any model output.
		// Function/custom declarations execute on the client and are replayable.
		switch tool.Get("type").String() {
		case "", "function", "custom", "namespace":
		default:
			return false
		}
	}
	return apiKeyFallbackReplayableValue(gjson.ParseBytes(body), 0)
}

func apiKeyFallbackReplayableValue(value gjson.Result, depth int) bool {
	if depth > 32 {
		return false
	}
	safe := true
	value.ForEach(func(key, child gjson.Result) bool {
		if key.String() == "type" {
			switch child.String() {
			case "image", "image_url", "input_image", "input_audio", "audio", "input_file", "file", "document", "video", "video_url", "input_video", "item_reference", "mcp", "computer", "computer_use_preview", "image_generation", "code_interpreter":
				safe = false
			}
		}
		if safe && (child.IsObject() || child.IsArray()) {
			safe = apiKeyFallbackReplayableValue(child, depth+1)
		}
		return safe
	})
	return safe
}

// prepareOpenAIGroupFallback starts again from the client's canonical body so
// the primary group's effort cap and channel alias cannot leak into the target.
func (h *OpenAIGatewayHandler) prepareOpenAIGroupFallback(c *gin.Context, key *service.APIKey, canonical []byte, model string, messages bool) ([]byte, service.ChannelMappingResult, error) {
	body := append([]byte(nil), canonical...)
	var err error
	if messages {
		if !allowOpenAICompatibleMessagesDispatch(c, key) {
			return nil, service.ChannelMappingResult{}, service.ErrGroupNotAllowed
		}
		c.Request = c.Request.WithContext(service.WithOpenAIReasoningEffortPolicy(c.Request.Context(), "", nil, ""))
		bindOpenAIReasoningEffortPolicyForMessagesRequest(c, key, body)
		// Validate a rejecting policy before billing/RPM admission. The bridge
		// applies the bound mapping once, against the untouched canonical effort.
		maxEffort, mappings, overLimit, _ := openAIReasoningEffortPolicyForRequest(c, key)
		_, _, err = service.ApplyReasoningEffortPolicy(body, maxEffort, mappings, overLimit)
	} else {
		body, _, err = applyOpenAIReasoningEffortPolicyForRequest(c, key, body)
		if strings.HasSuffix(c.Request.URL.Path, "/responses") {
			body, _ = normalizeCodexAutomationBootstrap(body)
			body, _ = normalizeCodexDelegationBootstrap(body)
		}
	}
	if err != nil {
		return nil, service.ChannelMappingResult{}, err
	}
	if err := checkAPIKeyFallbackSecurityAudit(h, c, key, model, body); err != nil {
		return nil, service.ChannelMappingResult{}, err
	}
	mapping, restricted := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), key.GroupID, model)
	if restricted {
		return nil, service.ChannelMappingResult{}, service.ErrGroupNotAllowed
	}
	return body, mapping, nil
}
