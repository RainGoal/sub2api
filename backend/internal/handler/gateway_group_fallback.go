package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// tryGatewayGroupFallback prepares the destination without writing a response.
// The caller keeps its account exclusions and original request pricing instant.
func (h *GatewayHandler) tryGatewayGroupFallback(c *gin.Context, fallback *apiKeyGroupFallback, current *service.APIKey, fs *FailoverState, model string, canonical []byte, parsed *service.ParsedRequest, cause apiKeyGroupFallbackCause) (*service.APIKey, *service.ParsedRequest, service.ChannelMappingResult) {
	var nextParsed *service.ParsedRequest
	var mapping service.ChannelMappingResult
	key, err := fallback.Try(c, current, model, &fs.SwitchCount, cause, func(trial *gin.Context, target *service.APIKey) error {
		if err := checkAPIKeyFallbackSecurityAudit(h, trial, target, model, canonical); err != nil {
			return err
		}
		var policyErr error
		var body []byte
		if strings.HasSuffix(trial.Request.URL.Path, "/messages") {
			body, _, policyErr = applyAnthropicReasoningEffortPolicyForRequest(trial, target, canonical)
		} else {
			if target.Group.ClaudeCodeOnly {
				return service.ErrGroupNotAllowed
			}
			body, _, policyErr = applyOpenAIReasoningEffortPolicyForRequest(trial, target, canonical)
		}
		if policyErr != nil {
			return policyErr
		}
		nextParsed, policyErr = parsed.CloneForBody(body)
		if policyErr != nil {
			return policyErr
		}
		nextParsed.GroupID = target.GroupID
		var restricted bool
		mapping, restricted = h.gatewayService.ResolveChannelMappingAndRestrict(trial.Request.Context(), target.GroupID, model)
		if restricted {
			return service.ErrGroupNotAllowed
		}
		ctx := service.WithPrefetchedStickySession(trial.Request.Context(), 0, *target.GroupID, h.metadataBridgeEnabled())
		ctx = service.WithSingleAccountRetry(ctx, false, h.metadataBridgeEnabled())
		trial.Request = trial.Request.WithContext(ctx)
		return nil
	})
	if err != nil {
		logger.FromContext(c.Request.Context()).Warn("gateway.api_key_fallback_rejected", zap.Int64("api_key_id", current.ID), zap.Error(err))
	}
	if key == nil {
		return nil, nil, service.ChannelMappingResult{}
	}
	fs.MaxSwitches = fallback.maxSwitches
	fs.LastFailoverErr = nil
	fs.hasBoundSession = false
	logger.FromContext(c.Request.Context()).Info("gateway.api_key_group_fallback",
		zap.Int64("api_key_id", key.ID), zap.Int64("primary_group_id", fallback.primaryID),
		zap.Int64("fallback_group_id", *key.GroupID), zap.Int("switch_count", fs.SwitchCount))
	return key, nextParsed, mapping
}
