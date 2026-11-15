package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIBlankStreamMaxBytes  = 256 * 1024
	openAIBlankStreamMaxWait   = 30 * time.Second
	openAIBlankStreamPolicyKey = "openai_blank_stream_policy"
)

var errOpenAIBlankStreamTimeout = errors.New("openai blank stream produced no semantic output before the deadline")

type openAIBlankStreamPolicy struct {
	settings OpenAICollaborationSettings
	userID   int64
	keyID    int64
	enabled  bool
}

// Snapshot once per logical request, including disabled policy. Account retries
// use the same snapshot; a new WebSocket turn must explicitly reset it.
func (s *OpenAIGatewayService) beginBlankStreamPolicy(ctx context.Context, c *gin.Context, account *Account, body []byte, newTurn bool) {
	if c == nil {
		return
	}
	if _, exists := c.Get(openAIBlankStreamPolicyKey); exists && !newTurn {
		return
	}
	policy := &openAIBlankStreamPolicy{}
	c.Set(openAIBlankStreamPolicyKey, policy)
	if s == nil || account == nil || !account.IsOpenAI() {
		return
	}
	key := getAPIKeyFromContext(c)
	if key == nil || key.GroupID == nil || key.ID <= 0 || key.UserID <= 0 {
		return
	}
	policy.settings = s.collaborationPolicy(ctx, c, account).clone()
	policy.userID, policy.keyID = key.UserID, key.ID
	policy.enabled = policy.settings.Enabled && policy.settings.BlankStreamFailoverEnabled && policy.settings.Allows(*key.GroupID, key.ID)
	if policy.enabled && (!gjson.ValidBytes(body) || collaborationHasHostedTools(body)) {
		policy.enabled = false
	}
}

// Account/Codex adaptation may inject hosted tools after the request snapshot.
// The final wire body can only narrow replay eligibility for this request.
func noteBlankStreamUpstreamBody(c *gin.Context, body []byte) {
	if c == nil {
		return
	}
	value, _ := c.Get(openAIBlankStreamPolicyKey)
	policy, _ := value.(*openAIBlankStreamPolicy)
	if policy != nil && policy.enabled && (!gjson.ValidBytes(body) || collaborationHasHostedTools(body)) {
		policy.enabled = false
	}
}

type openAIBlankStreamGuard struct {
	mu          sync.Mutex
	ctx         context.Context
	body        io.Closer
	placeholder collaborationPlaceholder
	responseEnd time.Time
	deadline    time.Time
	wait        time.Duration
	timer       *time.Timer
	stopCancel  func() bool
	blocked     bool
	closed      bool
	finished    bool
	blankText   bool
	reasoning   string
	err         error
}

func (s *OpenAIGatewayService) newBlankStreamGuard(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, start time.Time, reasoning string) *openAIBlankStreamGuard {
	if s == nil || c == nil || account == nil || !account.IsOpenAI() || resp == nil || resp.Body == nil || isOpenAIResponsesCompactPath(c) {
		return nil
	}
	value, _ := c.Get(openAIBlankStreamPolicyKey)
	policy, _ := value.(*openAIBlankStreamPolicy)
	key := getAPIKeyFromContext(c)
	if policy == nil || !policy.enabled || key == nil || key.GroupID == nil || key.ID != policy.keyID || key.UserID != policy.userID || !policy.settings.Allows(*key.GroupID, key.ID) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	g := &openAIBlankStreamGuard{ctx: ctx, body: resp.Body, wait: openAIBlankStreamMaxWait, reasoning: reasoning}
	if timeout := s.openAIFirstOutputTimeout(reasoning); timeout > 0 {
		g.responseEnd = start.Add(timeout)
	}
	g.mu.Lock()
	g.stopCancel = context.AfterFunc(ctx, func() { g.interrupt(ctx.Err()) })
	g.mu.Unlock()
	return g
}

func (g *openAIBlankStreamGuard) stopLocked() {
	if g.timer != nil {
		g.timer.Stop()
	}
	if g.stopCancel != nil {
		g.stopCancel()
	}
}

// Close the body outside the mutex: network readers may need to unwind before
// Close returns. An event which wins the mutex first can disarm the watchdog.
func (g *openAIBlankStreamGuard) interrupt(err error) {
	g.mu.Lock()
	closeBody := !g.closed && !g.finished && !g.blocked && g.err == nil
	if closeBody {
		if canceled := g.ctx.Err(); canceled != nil {
			err = canceled
		}
		g.err = err
		g.stopLocked()
	}
	g.mu.Unlock()
	if closeBody {
		_ = g.body.Close()
	}
}

func (g *openAIBlankStreamGuard) pendingErrorLocked() error {
	if g.err != nil {
		return g.err
	}
	if g.closed || g.finished || g.blocked {
		return nil
	}
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if !g.deadline.IsZero() && !time.Now().Before(g.deadline) {
		return errOpenAIBlankStreamTimeout
	}
	return nil
}

// Observe returns true only for a validated prelude. A failure terminal returns
// false too; BlocksFailover distinguishes a safe failure from real/unknown output.
func (g *openAIBlankStreamGuard) Observe(payload []byte, eventType string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.finished || g.blocked {
		return false
	}
	if err := g.pendingErrorLocked(); err != nil {
		return false
	}
	eventType = strings.TrimSpace(eventType)
	typed := gjson.GetBytes(payload, "type")
	if eventType == "" {
		eventType = typed.String()
	}
	valid := gjson.ValidBytes(payload) && g.placeholder.responseMatches(payload) &&
		(!typed.Exists() || (typed.Type == gjson.String && typed.String() == eventType))
	prelude := false
	if valid {
		switch eventType {
		case "response.created", "response.queued", "response.in_progress", "response.metadata", "keepalive":
			prelude = blankStreamEnvelopeKnown(payload) && !g.placeholder.hasOutput(payload)
		case "response.output_item.added", "response.output_item.done", "response.content_part.added", "response.content_part.done", "response.output_text.delta", "response.output_text.done":
			prelude = g.placeholder.accept(eventType, payload)
		case "response.failed", "error":
			if blankStreamEnvelopeKnown(payload) && !g.placeholder.hasOutput(payload) {
				if eventType == "response.failed" {
					g.finished = true
					g.stopLocked()
				}
				return false
			}
		}
	}
	if !prelude {
		g.blocked = true
		g.stopLocked()
		return false
	}
	if !openAIStreamEventIsMetadata(eventType) {
		for _, path := range []string{"delta", "text", "part.text", "item.content.0.text"} {
			value := gjson.GetBytes(payload, path)
			if value.Type == gjson.String && value.String() != "" && collaborationBlankText(value) {
				g.blankText = true
			}
		}
	}
	if g.placeholder.part && g.deadline.IsZero() {
		g.deadline = time.Now().Add(g.wait)
		if !g.responseEnd.IsZero() && g.responseEnd.Before(g.deadline) {
			g.deadline = g.responseEnd
		}
		g.timer = time.AfterFunc(time.Until(g.deadline), func() { g.interrupt(errOpenAIBlankStreamTimeout) })
	}
	return true
}

// Metadata values remain opaque, but unknown protocol fields cannot silently
// hide output while this guard is extending the replayable prelude.
func blankStreamEnvelopeKnown(payload []byte) bool {
	root := gjson.ParseBytes(payload)
	if !collaborationOnlyFields(root, "type sequence_number response response_id output error code message param metadata") {
		return false
	}
	response := root.Get("response")
	if response.Exists() && response.Raw != "null" && !collaborationOnlyFields(response,
		"id object created_at completed_at status background error incomplete_details instructions max_output_tokens max_tool_calls "+
			"model output parallel_tool_calls previous_response_id prompt prompt_cache_key prompt_cache_retention reasoning safety_identifier "+
			"service_tier store temperature text tool_choice tools top_logprobs top_p truncation usage user metadata conversation context_management") {
		return false
	}
	for _, path := range []string{"error", "response.error"} {
		value := root.Get(path)
		if value.Exists() && value.Raw != "null" && value.Type != gjson.String &&
			!collaborationOnlyFields(value, "code message type param status status_code http_status retry_after") {
			return false
		}
	}
	return true
}

func (g *openAIBlankStreamGuard) BlocksFailover() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blocked || g.ctx.Err() != nil
}

func (g *openAIBlankStreamGuard) HasBlankText() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blankText
}

func (s *OpenAIGatewayService) newBlankStreamTimeoutError(ctx context.Context, c *gin.Context, account *Account, guard *openAIBlankStreamGuard, startTime time.Time, originalModel string, headers http.Header) *UpstreamFailoverError {
	timeout, reasoning := openAIBlankStreamMaxWait, ""
	if guard != nil {
		guard.mu.Lock()
		timeout, reasoning = guard.wait, guard.reasoning
		if !guard.responseEnd.IsZero() && guard.deadline.Equal(guard.responseEnd) {
			timeout = guard.responseEnd.Sub(startTime)
		}
		guard.mu.Unlock()
	}
	return s.newOpenAIFirstOutputTimeoutError(ctx, c, account, opsUpstreamProxyID(account), opsUpstreamProxyName(account), startTime, originalModel, reasoning, timeout, "blank_prelude", headers)
}

// Err remains available after Close/EOF so a watchdog-induced EOF cannot be
// mistaken for a successful or ordinary truncated stream.
func (g *openAIBlankStreamGuard) Err() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	err := g.pendingErrorLocked()
	g.mu.Unlock()
	if err != nil {
		g.interrupt(err)
	}
	return err
}

func (g *openAIBlankStreamGuard) Close() {
	if g == nil {
		return
	}
	// Capture an already elapsed deadline even if its callback has not run.
	_ = g.Err()
	g.mu.Lock()
	g.closed = true
	g.stopLocked()
	g.mu.Unlock()
}
