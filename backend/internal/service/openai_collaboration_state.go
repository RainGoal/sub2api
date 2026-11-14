package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAICollaborationContextKey = "openai_collaboration_attempt"
const openAICollaborationTTL = 24 * time.Hour

// Cache entries contain hashes and tool identities, never message bodies.
// They are process-local; a miss simply uses error-triggered recovery again.
type openAICollaborationCache struct {
	mu      sync.Mutex
	entries map[string]openAICollaborationCacheEntry
}

type openAICollaborationCacheEntry struct {
	expires   time.Time
	plaintext map[string]bool
	omissions map[string]bool
	aliases   map[string]openAICollaborationTool
}

type openAICollaborationTool struct {
	Namespace string
	Name      string
}

type openAICollaborationAttempt struct {
	policy                       OpenAICollaborationSettings
	scope                        string
	branch                       string
	cache                        *openAICollaborationCache
	aliases                      map[string]openAICollaborationTool
	knownPlaintext               map[string]bool
	knownOmissions               map[string]bool
	omissions                    []openAICollaborationOmission
	recoveryUsed                 bool
	completed                    bool
	businessFailed               bool
	deliveryFailed               bool
	stopFailover                 bool
	preparedBodyHash             string
	upstreamAttempts             int
	probeResponseDeadlineReached bool
}

func collaborationHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *OpenAIGatewayService) collaborationPolicy(ctx context.Context, c *gin.Context, account *Account) OpenAICollaborationSettings {
	if s == nil || s.settingService == nil || account == nil || !account.IsOpenAI() || isOpenAIResponsesCompactPath(c) {
		return OpenAICollaborationSettings{}
	}
	key := getAPIKeyFromContext(c)
	if key == nil || key.GroupID == nil || key.ID <= 0 || key.UserID <= 0 {
		return OpenAICollaborationSettings{}
	}
	policy := s.settingService.GetOpenAICollaborationSettingsCached(ctx)
	scopePolicy := policy
	scopePolicy.Enabled = true
	if !scopePolicy.Allows(*key.GroupID, key.ID) {
		return OpenAICollaborationSettings{}
	}
	return policy
}

func collaborationAttempt(c *gin.Context) *openAICollaborationAttempt {
	if c == nil {
		return nil
	}
	value, _ := c.Get(openAICollaborationContextKey)
	attempt, _ := value.(*openAICollaborationAttempt)
	return attempt
}

func (s *OpenAIGatewayService) beginCollaborationAttempt(ctx context.Context, c *gin.Context, account *Account, body []byte, newTurn bool) *openAICollaborationAttempt {
	if c == nil || s == nil {
		return nil
	}
	const identityKey = "openai_collaboration_attempt_identity"
	prior := collaborationAttempt(c)
	budget := collaborationRequestBudget(c)
	if newTurn {
		prior, budget = nil, nil
		c.Set(openAICollaborationBudgetContextKey, (*openAICollaborationRequestBudget)(nil))
	} else if prior != nil {
		if budget == nil {
			budget = &openAICollaborationRequestBudget{upstreamAttempts: prior.upstreamAttempts}
		}
		budget.recoveryUsed = budget.recoveryUsed || prior.recoveryUsed
		budget.stopFailover = budget.stopFailover || prior.stopFailover
		c.Set(openAICollaborationBudgetContextKey, budget)
	}
	// A group outside the rollout has no active adapter, while its sends still
	// consume an existing logical-request budget.
	c.Set(openAICollaborationContextKey, (*openAICollaborationAttempt)(nil))
	if account == nil || !account.IsOpenAI() {
		return nil
	}
	key := getAPIKeyFromContext(c)
	if key == nil || key.GroupID == nil || key.ID <= 0 || key.UserID <= 0 {
		return nil
	}
	// Explicit client identity is always namespaced by authenticated ownership.
	// Content-derived sticky hashes are not used as collaboration identity.
	session := explicitOpenAIRequestSessionID(c, body)
	branch := resolveOpenAIWSClientThreadID(c, body)
	if session == "" {
		session = branch
	}
	scope := ""
	if session != "" {
		scope = collaborationHash(fmt.Sprintf("collaboration:v1:%d:%d:%d:%s", key.UserID, key.ID, *key.GroupID, session))
	}
	policy := s.collaborationPolicy(ctx, c, account)
	if len(policy.GroupIDs) == 0 {
		return nil
	}
	identity := collaborationHash(fmt.Sprintf("%d:%d:%d:%q:%q", key.UserID, key.ID, *key.GroupID, session, branch))
	if prior != nil && c.GetString(identityKey) == identity {
		prior.policy = policy
		c.Set(openAICollaborationContextKey, prior)
		return prior
	}
	if budget == nil {
		budget = &openAICollaborationRequestBudget{}
	}
	entry := s.collaborationCache.read(scope)
	attempt := &openAICollaborationAttempt{
		policy: policy, scope: scope, branch: branch, cache: &s.collaborationCache,
		aliases: entry.aliases, knownPlaintext: entry.plaintext,
		recoveryUsed: budget.recoveryUsed, stopFailover: budget.stopFailover,
		upstreamAttempts: budget.upstreamAttempts,
	}
	if scope != "" && branch != "" {
		attempt.knownOmissions = s.collaborationCache.read(scope + ":" + branch).omissions
	}
	if attempt.aliases == nil {
		attempt.aliases = make(map[string]openAICollaborationTool)
	}
	c.Set(openAICollaborationContextKey, attempt)
	c.Set(identityKey, identity)
	c.Set(openAICollaborationBudgetContextKey, budget)
	return attempt
}

func (c *openAICollaborationCache) read(key string) openAICollaborationCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.entries[key]
	if !found || key == "" || time.Now().After(entry.expires) {
		delete(c.entries, key)
		return openAICollaborationCacheEntry{}
	}
	copyEntry := openAICollaborationCacheEntry{plaintext: map[string]bool{}, omissions: map[string]bool{}, aliases: map[string]openAICollaborationTool{}}
	for k, v := range entry.plaintext {
		copyEntry.plaintext[k] = v
	}
	for k, v := range entry.omissions {
		copyEntry.omissions[k] = v
	}
	for k, v := range entry.aliases {
		copyEntry.aliases[k] = v
	}
	return copyEntry
}

func (c *openAICollaborationCache) update(key string, mutate func(*openAICollaborationCacheEntry)) {
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]openAICollaborationCacheEntry)
	}
	entry, found := c.entries[key]
	if !found && len(c.entries) >= 2048 {
		for k, v := range c.entries {
			if time.Now().After(v.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= 2048 {
			return
		}
	}
	if time.Now().After(entry.expires) {
		entry = openAICollaborationCacheEntry{plaintext: map[string]bool{}, omissions: map[string]bool{}, aliases: map[string]openAICollaborationTool{}}
	}
	mutate(&entry)
	entry.expires = time.Now().Add(openAICollaborationTTL)
	c.entries[key] = entry
}

func collaborationOmissionKey(messageID, digest string) string { return messageID + ":" + digest }

func (a *openAICollaborationAttempt) finish(success bool) {
	if a == nil || !success || !a.completed || a.deliveryFailed || len(a.omissions) == 0 || a.scope == "" || a.branch == "" || a.cache == nil || !a.policy.Enabled {
		return
	}
	a.cache.update(a.scope+":"+a.branch, func(entry *openAICollaborationCacheEntry) {
		for _, omitted := range a.omissions {
			if len(entry.omissions) < 512 {
				entry.omissions[collaborationOmissionKey(omitted.MessageID, omitted.Digest)] = true
			}
		}
	})
}

func collaborationDeliverySucceeded(ctx context.Context, c *gin.Context, result *OpenAIForwardResult, err error) bool {
	return err == nil && result != nil && !result.ClientDisconnect &&
		(ctx == nil || ctx.Err() == nil) && (c == nil || c.Request == nil || c.Request.Context().Err() == nil)
}

// Called with the exact body that was sent upstream, before any downstream
// headers or events are committed. No attempt is retried through failover.
func (s *OpenAIGatewayService) recoverCollaborationResponse(ctx context.Context, c *gin.Context, account *Account, body []byte, resp *http.Response, probeDeadlines ...time.Time) ([]byte, bool) {
	a := collaborationAttempt(c)
	if a != nil {
		a.probeResponseDeadlineReached = false
	}
	if a == nil || !a.policy.Enabled || !a.policy.LegacyCipherRecoveryEnabled || (ctx != nil && ctx.Err() != nil) {
		return body, false
	}
	// Re-read the live master switch before starting another paid attempt.
	live := s.collaborationPolicy(ctx, c, account)
	if !live.Enabled || !live.LegacyCipherRecoveryEnabled {
		return body, false
	}
	if !bytesContainAgentCipher(body) {
		return body, false
	}
	blankWait := openAICollaborationPreludeWait
	if resp != nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) &&
		!a.recoveryUsed && !a.stopFailover && collaborationPriorUpstreamAttempts(c) <= 1 &&
		(resp.StatusCode != http.StatusOK || !collaborationHasHostedTools(body)) &&
		collaborationHasUnknownAgentCipher(body, a.knownPlaintext) {
		blankWait = live.BlankPreludeWait()
	}
	probe := probeOpenAICollaborationResponseWithWait(ctx, resp, blankWait, probeDeadlines...)
	a.probeResponseDeadlineReached = probe.Reason == "response_deadline"
	if probe.BlankPrelude || probe.Reason == "prelude_deadline" || probe.Reason == "response_deadline" || probe.Reason == "prelude_size_limit" {
		// A probe release is not necessarily a downstream write or a failed
		// request. Keep this diagnostic out of upstream error accounting.
		slog.Info("OpenAI collaboration prelude released", "account_id", account.ID,
			"prelude_release_reason", probe.Reason, "probe_elapsed_ms", probe.Elapsed.Milliseconds(),
			"blank_prelude", probe.BlankPrelude, "blank_prelude_wait_ms", blankWait.Milliseconds(),
			"upstream_attempts", collaborationPriorUpstreamAttempts(c))
	}
	failure := probe.Failure
	if len(failure) == 0 {
		return body, false
	}
	// A longer prelude can span a live shutdown. Preserve the original
	// response when recovery was disabled while waiting.
	live = s.collaborationPolicy(ctx, c, account)
	if (ctx != nil && ctx.Err() != nil) || !live.Enabled || !live.LegacyCipherRecoveryEnabled {
		return body, false
	}
	recovery, err := recoverOpenAICollaborationEncryptedContentWithPlaintext(body, resp.StatusCode, failure, true, a.knownPlaintext)
	if err != nil {
		a.stopFailover = true
		return body, false
	}
	if recovery == nil {
		return body, false
	}
	a.stopFailover = true
	if a.recoveryUsed || collaborationPriorUpstreamAttempts(c) > 1 ||
		(resp.StatusCode == http.StatusOK && collaborationHasHostedTools(body)) {
		return body, false
	}
	a.recoveryUsed = true
	a.omissions = append(a.omissions, recovery.Omissions...)
	_ = resp.Body.Close()
	accounting, accountingErr := s.recordCollaborationFailedAttemptUsage(ctx, c, account, body, resp, failure)
	usage, usageKnown := collaborationFailedAttemptUsage(failure)
	// Supplier costs belong to the replaced attempt, never the successful
	// customer bill. Diagnostics retain explicit unknown/failed accounting.
	diagnostic := map[string]any{"selection": recovery.Selection, "omitted_blocks": len(recovery.Omissions),
		"task_state": "needs_reassignment", "usage_known": usageKnown,
		"accounting": accounting, "business_outcome": "failed_replaced",
		"probe_elapsed_ms": probe.Elapsed.Milliseconds(), "prelude_release_reason": probe.Reason,
		"recovery_attempts": 1}
	if usageKnown {
		diagnostic["usage"] = usage
	}
	if accountingErr != nil {
		diagnostic["accounting_error"] = truncateString(accountingErr.Error(), 256)
	}
	details, _ := json.Marshal(diagnostic)
	// Ops Message is capped at 2048 characters. Keep accounting independently
	// readable even when a long history selects many agent blocks.
	sample := make([]map[string]any, 0, min(len(recovery.Omissions), 8))
	for _, omission := range recovery.Omissions[:min(len(recovery.Omissions), 8)] {
		sample = append(sample, map[string]any{"message_id": truncateString(omission.MessageID, 128), "digest": omission.Digest,
			"input_index": omission.InputIndex, "content_index": omission.ContentIndex})
	}
	omissionDetail, _ := json.Marshal(map[string]any{"omissions": sample, "truncated": len(sample) != len(recovery.Omissions)})
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"), Kind: "collaboration_lossy_retry", Message: string(details), Detail: string(omissionDetail)})
	return recovery.Body, true
}

func bytesContainAgentCipher(body []byte) bool {
	return strings.Contains(string(body), `"agent_message"`) && strings.Contains(string(body), `"encrypted_content"`)
}

func collaborationHasUnknownAgentCipher(body []byte, knownPlaintext map[string]bool) bool {
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("type").String() != "agent_message" {
			continue
		}
		for _, part := range item.Get("content").Array() {
			cipher := part.Get("encrypted_content")
			if part.Get("type").String() == "encrypted_content" && cipher.Type == gjson.String &&
				cipher.String() != "" && !knownPlaintext[collaborationHash(cipher.String())] {
				return true
			}
		}
	}
	return false
}

func collaborationHasPlaintextAgentMessage(body []byte) bool {
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("type").String() != "agent_message" {
			continue
		}
		parts := item.Get("content").Array()
		plain := len(parts) > 0
		for _, part := range parts {
			kind := part.Get("type").String()
			if (kind != "input_text" && kind != "text") || part.Get("text").Type != gjson.String {
				plain = false
			}
		}
		if plain {
			return true
		}
	}
	return false
}

func collaborationRecoveryOwnsFailure(c *gin.Context) bool {
	a := collaborationAttempt(c)
	return a != nil && (a.stopFailover || a.recoveryUsed)
}

func collaborationCanStartRecovery(c *gin.Context) bool {
	a := collaborationAttempt(c)
	return a == nil || !a.policy.Enabled || (!collaborationRecoveryOwnsFailure(c) && collaborationPriorUpstreamAttempts(c) <= 1)
}

func (s *OpenAIGatewayService) collaborationRetryError(ctx context.Context, c *gin.Context, account *Account) error {
	a := collaborationAttempt(c)
	if a == nil || !a.recoveryUsed {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if a.policy.LegacyCipherRecoveryEnabled {
		live := s.collaborationPolicy(ctx, c, account)
		if !live.Enabled || !live.LegacyCipherRecoveryEnabled {
			return fmt.Errorf("collaboration recovery was disabled before retry")
		}
	}
	return nil
}

func noteCollaborationEncryptedRecovery(c *gin.Context) {
	if a := collaborationAttempt(c); a != nil && a.policy.Enabled {
		a.recoveryUsed = true
		a.stopFailover = true
	}
}

// An empty failed response does not prove a hosted tool had no side effects.
// Only declarations known to execute on the client are safe to replay.
func collaborationHasHostedTools(body []byte) bool {
	var hosted func(gjson.Result) bool
	hosted = func(list gjson.Result) bool {
		if !list.Exists() {
			return false
		}
		if !list.IsArray() {
			return true
		}
		for _, tool := range list.Array() {
			switch tool.Get("type").String() {
			case "function", "custom":
			case "namespace":
				if hosted(tool.Get("tools")) || hosted(tool.Get("children")) {
					return true
				}
			default:
				return true
			}
		}
		return false
	}
	if hosted(gjson.GetBytes(body, "tools")) {
		return true
	}
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("type").String() == "additional_tools" && (hosted(item.Get("tools")) || hosted(item.Get("additional_tools"))) {
			return true
		}
	}
	return false
}

func applyRememberedCollaborationOmissions(body []byte, a *openAICollaborationAttempt) ([]byte, error) {
	if a == nil || !a.policy.Enabled || !a.policy.LegacyCipherRecoveryEnabled || len(a.knownOmissions) == 0 || !bytesContainAgentCipher(body) {
		return body, nil
	}
	var request map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &request); err != nil {
		return nil, err
	}
	input, _ := request["input"].([]any)
	var selected []openAICollaborationOmission
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "agent_message" {
			continue
		}
		id, _ := item["id"].(string)
		content, _ := item["content"].([]any)
		for j, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok || part["type"] != "encrypted_content" {
				continue
			}
			cipher, ok := part["encrypted_content"].(string)
			if !ok || cipher == "" {
				continue
			}
			digest := collaborationHash(cipher)
			if !a.knownPlaintext[digest] && a.knownOmissions[collaborationOmissionKey(id, digest)] {
				selected = append(selected, openAICollaborationOmission{MessageID: id, InputIndex: i, ContentIndex: j, Digest: digest})
			}
		}
	}
	if len(selected) == 0 {
		return body, nil
	}
	applyOpenAICollaborationOmissions(input, selected)
	a.omissions = append(a.omissions, selected...)
	return marshalOpenAIUpstreamJSON(request)
}
