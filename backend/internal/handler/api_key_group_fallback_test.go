package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type fallbackResolverStub struct {
	group *service.Group
	err   error
	calls int
}

func (s *fallbackResolverStub) ResolveFallbackGroup(context.Context, *service.APIKey) (*service.Group, error) {
	s.calls++
	return s.group, s.err
}

type fallbackBillingStub struct {
	err   error
	calls int
	key   *service.APIKey
}

func (s *fallbackBillingStub) CheckFallbackBillingEligibility(_ context.Context, _ *service.User, key *service.APIKey, _ *service.Group, _ string) error {
	s.calls++
	s.key = key
	return s.err
}

func fallbackTestState(t *testing.T) (*gin.Context, *service.APIKey, *apiKeyGroupFallback, *fallbackResolverStub, *fallbackBillingStub) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	primary, backup, rpm := int64(1), int64(2), 7
	key := &service.APIKey{ID: 9, GroupID: &primary, FallbackGroupID: &backup,
		Group: &service.Group{ID: primary, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard},
		User:  &service.User{ID: 5, UserGroupRPMOverride: &rpm}}
	c.Set(string(middleware2.ContextKeyAPIKey), key)
	resolver := &fallbackResolverStub{group: &service.Group{ID: backup, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard}}
	billing := &fallbackBillingStub{}
	f := newAPIKeyGroupFallback(c, resolver, billing, key, []byte(`{"model":"gpt-5.2","input":"hello"}`), 3)
	return c, key, f, resolver, billing
}

func TestAPIKeyGroupFallbackPreservesIdentityBudgetAndDeadline(t *testing.T) {
	c, key, f, resolver, billing := fallbackTestState(t)
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(c.Request.Context(), deadline)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	switches := 1
	next, err := f.Try(c, key, "gpt-5.2", &switches, apiKeyGroupFallbackCause{UpstreamErr: &service.UpstreamFailoverError{StatusCode: 503}}, func(trial *gin.Context, next *service.APIKey) error {
		trial.Set("prepared", next.Group.ID)
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, key.ID, next.ID)
	require.Equal(t, int64(2), *next.GroupID)
	require.Equal(t, int64(1), *key.GroupID)
	require.NotSame(t, key.User, next.User)
	require.Nil(t, next.User.UserGroupRPMOverride)
	require.Equal(t, 7, *key.User.UserGroupRPMOverride)
	require.Nil(t, next.FallbackGroupID)
	require.Equal(t, 2, switches)
	require.Equal(t, 1, f.PrimarySwitchLimit())
	actualDeadline, ok := c.Request.Context().Deadline()
	require.True(t, ok)
	require.Equal(t, deadline, actualDeadline)
	bound, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	require.Same(t, next, bound)
	require.Same(t, next, billing.key)
	require.Equal(t, int64(2), c.GetInt64("prepared"))
	second, err := f.Try(c, next, "gpt-5.2", &switches, apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, nil)
	require.NoError(t, err)
	require.Nil(t, second)
	require.Equal(t, 1, resolver.calls)
}

func TestAPIKeyGroupFallbackRejectedTargetDoesNotMutateRequest(t *testing.T) {
	for _, stage := range []string{"authorization", "prepare", "billing"} {
		t.Run(stage, func(t *testing.T) {
			c, key, f, resolver, billing := fallbackTestState(t)
			original := c.Request
			blocked := errors.New("target rejected")
			if stage == "authorization" {
				resolver.err = blocked
			}
			if stage == "billing" {
				billing.err = blocked
			}
			switches := 0
			next, err := f.Try(c, key, "gpt-5.2", &switches, apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, func(trial *gin.Context, _ *service.APIKey) error {
				trial.Set("should_not_leak", true)
				trial.Request.Header.Set("Anthropic-Beta", "target-only")
				if stage == "prepare" {
					return blocked
				}
				return nil
			})
			require.ErrorIs(t, err, blocked)
			require.Nil(t, next)
			require.Same(t, original, c.Request)
			require.False(t, c.GetBool("should_not_leak"))
			require.Empty(t, c.Request.Header.Get("Anthropic-Beta"))
			require.Equal(t, 0, switches)
			bound, _ := middleware2.GetAPIKeyFromContext(c)
			require.Same(t, key, bound)
		})
	}
}

func TestAPIKeyGroupFallbackRestoresClientHeaders(t *testing.T) {
	c, key, _, resolver, billing := fallbackTestState(t)
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	fallback := newAPIKeyGroupFallback(c, resolver, billing, key, []byte(`{"model":"gpt-5.2","input":"hello"}`), 3)
	primaryRequest := c.Request
	// Simulate the primary's Bedrock compatibility policy removing this token.
	primaryRequest.Header.Del("Anthropic-Beta")
	switches := 0
	next, err := fallback.Try(c, key, "gpt-5.2", &switches, apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, func(trial *gin.Context, _ *service.APIKey) error {
		require.Equal(t, "interleaved-thinking-2025-05-14", trial.Request.Header.Get("Anthropic-Beta"))
		trial.Request.Header.Set("X-Target-Policy", "applied")
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, "interleaved-thinking-2025-05-14", c.Request.Header.Get("Anthropic-Beta"))
	require.Equal(t, "applied", c.Request.Header.Get("X-Target-Policy"))
	require.Empty(t, primaryRequest.Header.Get("Anthropic-Beta"))
	require.Empty(t, primaryRequest.Header.Get("X-Target-Policy"))
}

func TestAPIKeyGroupFallbackStopsUnsafeReplay(t *testing.T) {
	for _, reason := range []string{"canceled", "written", "budget", "forced_platform", "wrong_group", "disabled", "websocket", "allowlist"} {
		t.Run(reason, func(t *testing.T) {
			c, key, f, resolver, _ := fallbackTestState(t)
			switches := 0
			switch reason {
			case "canceled":
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			case "written":
				_, _ = c.Writer.Write([]byte("data: token\n\n"))
			case "budget":
				switches = 3
			case "forced_platform":
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, service.PlatformAntigravity))
			case "wrong_group":
				id := int64(99)
				key.GroupID = &id
			case "disabled":
				f.enabled = false
			case "websocket":
				c.Request.Method = http.MethodGet
			case "allowlist":
				resolver.group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"other-model"}}
			}
			next, _ := f.Try(c, key, "gpt-5.2", &switches, apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, nil)
			require.Nil(t, next)
			if reason != "allowlist" {
				require.Zero(t, resolver.calls)
			}
		})
	}
}

func TestAPIKeyGroupFallbackClassifiesFailureWithoutBypassingPolicy(t *testing.T) {
	tests := []struct {
		name   string
		cause  apiKeyGroupFallbackCause
		profit bool
		want   bool
	}{
		{"empty capacity", apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, false, true},
		{"model", apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts, ModelNotFound: true}, false, false},
		{"profit final veto", apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts, ProfitVetoed: true}, false, false},
		{"profit ambiguous pool", apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, true, false},
		{"channel policy", apiKeyGroupFallbackCause{SelectionErr: fmt.Errorf("%w (channel pricing restriction)", service.ErrNoAvailableAccounts)}, false, false},
		{"channel policy after upstream failure", apiKeyGroupFallbackCause{SelectionErr: fmt.Errorf("%w (channel pricing restriction)", service.ErrNoAvailableAccounts), UpstreamErr: &service.UpstreamFailoverError{StatusCode: 503}}, false, false},
		{"database error", apiKeyGroupFallbackCause{SelectionErr: errors.New("database unavailable")}, false, false},
		{"provider capacity", apiKeyGroupFallbackCause{UpstreamErr: &service.UpstreamFailoverError{StatusCode: 503}}, false, true},
		{"upstream invalid payload", apiKeyGroupFallbackCause{UpstreamErr: &service.UpstreamFailoverError{StatusCode: 400}}, false, false},
		{"nonretryable", apiKeyGroupFallbackCause{UpstreamErr: &service.UpstreamFailoverError{StatusCode: 503, NextAccountAction: service.NextAccountStop}}, false, false},
		{"request policy", apiKeyGroupFallbackCause{UpstreamErr: &service.UpstreamFailoverError{StatusCode: 403, Scope: service.GatewayFailureScopeRequest}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, apiKeyFallbackCauseAllowed(&service.Group{ProfitControlEnabled: tt.profit}, tt.cause))
		})
	}
}

func TestAPIKeyGroupFallbackReplayableRequest(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.2","previous_response_id":"resp_1"}`,
		`{"model":"gpt-5.2","conversation":{"id":"conv_1"}}`,
		`{"model":"gpt-5.2","background":true}`,
		`{"model":"gpt-5.2","context_management":[{"type":"compaction"}]}`,
		`{"model":"gpt-5.2","input":[{"type":"item_reference","id":"msg_1"}]}`,
		`{"model":"gpt-5.2","messages":[{"content":[{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]}]}`,
		`{"model":"claude-sonnet-4-5","messages":[{"content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"test"}}]}]}`,
		`{"model":"gpt-5.2","messages":[{"content":[{"type":"video_url","video_url":{"url":"https://example.test/video.mp4"}}]}]}`,
		`{"model":"gpt-5.2","tools":[{"type":"mcp","server_url":"https://example.test"}]}`,
		`{"model":"gpt-5.2","modalities":["text","audio"]}`,
		`{"model":"gpt-5.2","tools":[{"type":"web_search"}]}`,
		`{"model":"gpt-image-1","input":"draw"}`,
	} {
		t.Run(body, func(t *testing.T) { require.False(t, apiKeyFallbackReplayableBody([]byte(body))) })
	}
	require.True(t, apiKeyFallbackReplayableBody([]byte(`{"model":"gpt-5.2","background":false,"input":"hello","tools":[{"type":"function","name":"read_file"}]}`)))
}

func TestAPIKeyGroupFallbackUnconfiguredKeepsOriginalBudget(t *testing.T) {
	c, key, _, resolver, billing := fallbackTestState(t)
	key.FallbackGroupID = nil
	require.Nil(t, captureAPIKeyFallbackBody(key, []byte(`{"model":"gpt-5.2"}`)))
	f := newAPIKeyGroupFallback(c, resolver, billing, key, []byte(`{"model":"gpt-5.2"}`), 10)
	require.False(t, f.enabled)
	require.Equal(t, 10, f.PrimarySwitchLimit())
	switches := 0
	next, err := f.Try(c, key, "gpt-5.2", &switches, apiKeyGroupFallbackCause{SelectionErr: service.ErrNoAvailableAccounts}, nil)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Zero(t, resolver.calls)
	require.Zero(t, billing.calls)
	require.Zero(t, switches)
}

func TestAPIKeyGroupFallbackUnsupportedGroupsKeepOriginalBudget(t *testing.T) {
	c, key, _, resolver, billing := fallbackTestState(t)
	for _, platform := range []string{service.PlatformGemini, service.PlatformComposite, service.PlatformAntigravity} {
		key.Group.Platform = platform
		require.False(t, newAPIKeyGroupFallback(c, resolver, billing, key, []byte(`{"model":"gpt-5.2"}`), 10).enabled)
	}
	key.Group.Platform = service.PlatformOpenAI
	key.Group.SubscriptionType = service.SubscriptionTypeSubscription
	require.False(t, newAPIKeyGroupFallback(c, resolver, billing, key, []byte(`{"model":"gpt-5.2"}`), 10).enabled)
	key.Group.SubscriptionType = service.SubscriptionTypeStandard
	key.Group.FallbackGroupID = key.FallbackGroupID
	require.False(t, newAPIKeyGroupFallback(c, resolver, billing, key, []byte(`{"model":"gpt-5.2"}`), 10).enabled)
}

func TestAPIKeyGroupFallbackForcedRouteKeepsOriginalBudget(t *testing.T) {
	c, _, fallback, _, _ := fallbackTestState(t)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, service.PlatformAntigravity))
	require.Equal(t, 3, fallback.PrimarySwitchLimit(c))
	require.False(t, fallback.enabled)
}

type fallbackHandlerGroupRepo struct {
	service.GroupRepository
	groups map[int64]*service.Group
}

func (r *fallbackHandlerGroupRepo) GetByID(_ context.Context, id int64) (*service.Group, error) {
	return r.groups[id], nil
}

func (r *fallbackHandlerGroupRepo) GetByIDLite(ctx context.Context, id int64) (*service.Group, error) {
	return r.GetByID(ctx, id)
}

type fallbackHandlerUserRepo struct{ service.UserRepository }

func (r *fallbackHandlerUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 5, Status: service.StatusActive}, nil
}

type fallbackHandlerAccountRepo struct {
	*openAIWSFailoverHandlerAccountRepoStub
	emptyPrimary bool
}

func (r *fallbackHandlerAccountRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	group, _ := ctx.Value(ctxkey.Group).(*service.Group)
	if group == nil {
		return nil, nil
	}
	return r.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, platform)
}

func (r *fallbackHandlerAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]service.Account, error) {
	if groupID == 1 && r.emptyPrimary {
		return nil, nil
	}
	var accounts []service.Account
	for _, account := range r.accounts {
		if account.GroupIDs[0] == groupID && account.Platform == platform {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (r *fallbackHandlerAccountRepo) ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, _ bool) ([]service.Account, error) {
	if groupID == nil {
		return r.ListSchedulableByPlatform(ctx, platforms[0])
	}
	return r.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platforms[0])
}

type fallbackHandlerUpstream struct {
	service.HTTPUpstream
	accountIDs []int64
	bodies     [][]byte
}

func (u *fallbackHandlerUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.bodies = append(u.bodies, body)
	u.accountIDs = append(u.accountIDs, accountID)
	status, response := http.StatusBadGateway, `{"error":{"message":"temporary upstream failure"}}`
	if accountID == 12 {
		status, response = http.StatusOK, `{"id":"resp_backup","object":"response","model":"gpt-5.2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
		if strings.HasSuffix(req.URL.Path, "/chat/completions") {
			response = `{"id":"chatcmpl_backup","object":"chat.completion","model":"gpt-5.2","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestAPIKeyGroupFallbackHTTPBillsTargetAndReappliesCanonicalEffort(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		for _, emptyPrimary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/empty_primary=%t", endpoint, emptyPrimary), func(t *testing.T) {
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Gateway.MaxAccountSwitches = 1
				groups := &fallbackHandlerGroupRepo{groups: map[int64]*service.Group{
					1: {ID: 1, Hydrated: true, Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1, MaxReasoningEffort: "low"},
					2: {ID: 2, Hydrated: true, Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 2, MaxReasoningEffort: "high"},
				}}
				users := &fallbackHandlerUserRepo{}
				groups.groups[1].AllowMessagesDispatch = true
				groups.groups[2].AllowMessagesDispatch = true
				accounts := &fallbackHandlerAccountRepo{emptyPrimary: emptyPrimary, openAIWSFailoverHandlerAccountRepoStub: &openAIWSFailoverHandlerAccountRepoStub{}}
				for i := int64(1); i <= 2; i++ {
					accounts.accounts = append(accounts.accounts, service.Account{ID: i + 10, GroupIDs: []int64{i},
						Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
						Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test", "pool_mode_retry_count": float64(0)},
						Extra:       map[string]any{"openai_passthrough": true}})
					if endpoint != "responses" {
						accounts.accounts[len(accounts.accounts)-1].Extra = map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)}
					}
				}
				upstream := &fallbackHandlerUpstream{}
				usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				gateway := service.NewOpenAIGatewayService(accounts, usage, nil, nil, nil, nil, nil, cfg, nil, nil,
					service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				keys := service.NewAPIKeyService(nil, users, groups, nil, nil, nil, cfg)
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, keys, nil, nil, nil, nil, cfg)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				requestBody, effortPath := `{"model":"gpt-5.2","input":"hello","reasoning":{"effort":"high"}}`, "reasoning.effort"
				switch endpoint {
				case "chat/completions":
					requestBody, effortPath = `{"model":"gpt-5.2","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`, "reasoning_effort"
				case "messages":
					requestBody, effortPath = `{"model":"gpt-5.2","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"high"}}`, "reasoning_effort"
				}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(requestBody))
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, groups.groups[1]))
				primary, backup := int64(1), int64(2)
				key := &service.APIKey{ID: 9, UserID: 5, GroupID: &primary, FallbackGroupID: &backup,
					Group: groups.groups[1], User: &service.User{ID: 5, Status: service.StatusActive}}
				c.Set(string(middleware2.ContextKeyAPIKey), key)
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 5})
				switch endpoint {
				case "responses":
					h.Responses(c)
				case "chat/completions":
					h.ChatCompletions(c)
				case "messages":
					h.Messages(c)
				}
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				wantCalls := []int64{11, 12}
				if emptyPrimary {
					wantCalls = []int64{12}
				}
				require.Equal(t, wantCalls, upstream.accountIDs)
				require.Equal(t, "high", gjson.GetBytes(upstream.bodies[len(upstream.bodies)-1], effortPath).String())
				if !emptyPrimary {
					require.Equal(t, "low", gjson.GetBytes(upstream.bodies[0], effortPath).String())
				}
				require.Equal(t, int64(1), *key.GroupID, "cached key must keep its permanent primary binding")
				require.Len(t, usage.created, 1)
				log := <-usage.created
				require.Equal(t, int64(2), *log.GroupID)
				require.Equal(t, int64(9), log.APIKeyID)
				require.Equal(t, int64(12), log.AccountID)
				require.Equal(t, float64(2), log.RateMultiplier)
			})
		}
	}
}

type fallbackSecurityAuditStub struct {
	cached  bool
	groupID int64
}

func (s *fallbackSecurityAuditStub) checkSecurityAudit(c *gin.Context, _ *zap.Logger, key *service.APIKey, _ middleware2.AuthSubject, _, _ string, _ []byte) *securityaudit.Decision {
	s.cached = c.GetBool(securityAuditCompletedContextKey)
	s.groupID = *key.GroupID
	return &securityaudit.Decision{AllowNextStage: false}
}

func TestAPIKeyGroupFallbackRechecksTargetSecurityPolicy(t *testing.T) {
	c, key, _, resolver, _ := fallbackTestState(t)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.User.ID})
	c.Set(securityAuditCompletedContextKey, true)
	auditor := &fallbackSecurityAuditStub{}
	targetKey := cloneAPIKeyWithGroup(key, resolver.group)
	err := checkAPIKeyFallbackSecurityAudit(auditor, c, targetKey, "gpt-5.2", []byte(`{"model":"gpt-5.2","input":"hello"}`))
	require.ErrorIs(t, err, service.ErrGroupNotAllowed)
	require.False(t, auditor.cached, "a primary group's audit decision cannot authorize the backup group's blocking scanners")
	require.Equal(t, int64(2), auditor.groupID)
	require.False(t, c.Writer.Written())
}
