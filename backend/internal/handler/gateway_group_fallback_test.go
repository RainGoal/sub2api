//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type gatewayFallbackGroupRepo struct {
	service.GroupRepository
	groups map[int64]*service.Group
}

func (r *gatewayFallbackGroupRepo) GetByID(_ context.Context, id int64) (*service.Group, error) {
	return r.groups[id], nil
}

func (r *gatewayFallbackGroupRepo) GetByIDLite(ctx context.Context, id int64) (*service.Group, error) {
	return r.GetByID(ctx, id)
}

type gatewayFallbackUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *gatewayFallbackUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

type gatewayFallbackSchedulerCache struct {
	*fakeSchedulerCache
	visited []int64
}

func (s *gatewayFallbackSchedulerCache) GetSnapshot(_ context.Context, bucket service.SchedulerBucket) ([]*service.Account, bool, error) {
	s.visited = append(s.visited, bucket.GroupID)
	var out []*service.Account
	for _, account := range s.accounts {
		for _, membership := range account.AccountGroups {
			if membership.GroupID == bucket.GroupID {
				out = append(out, account)
				break
			}
		}
	}
	return out, true, nil
}

type gatewayFallbackUpstream struct {
	service.HTTPUpstream
	accounts []int64
	failed   map[int64]bool
}

func (u *gatewayFallbackUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func (u *gatewayFallbackUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.accounts = append(u.accounts, accountID)
	if u.failed[accountID] {
		return &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"api_error","message":"temporary upstream failure"}}`))}, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	response := `{"id":"msg_fallback","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"fallback ok"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":10}}`
	contentType := "application/json"
	if gjson.GetBytes(body, "stream").Bool() {
		response = gatewayFallbackSSE
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

const gatewayFallbackSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_fallback","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":100,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fallback ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":10}}

event: message_stop
data: {"type":"message_stop"}

`

func newGatewayFallbackTestHandler(t *testing.T) (*GatewayHandler, *service.APIKey, *gatewayFallbackSchedulerCache, *gatewayFallbackUpstream, <-chan *service.UsageLog) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	primary := &service.Group{ID: 11, Platform: service.PlatformAnthropic, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	backup := &service.Group{ID: 12, Platform: service.PlatformAnthropic, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 2}
	groups := &gatewayFallbackGroupRepo{groups: map[int64]*service.Group{11: primary, 12: backup}}
	user := &service.User{ID: 21, Status: service.StatusActive, Balance: 100}
	key := &service.APIKey{ID: 31, UserID: user.ID, User: user, GroupID: &primary.ID, Group: primary, FallbackGroupID: &backup.ID, Status: service.StatusActive}
	account := &service.Account{
		ID: 41, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"api_key": "test-fallback-credential"},
		AccountGroups: []service.AccountGroup{{AccountID: 41, GroupID: backup.ID}},
		GroupIDs:      []int64{backup.ID},
	}
	scheduler := &gatewayFallbackSchedulerCache{fakeSchedulerCache: &fakeSchedulerCache{accounts: []*service.Account{account}}}
	// Keep group-scoped scheduling while the billing fixture skips DB writes.
	snapshot := service.NewSchedulerSnapshotService(scheduler, nil, nil, groups, &config.Config{})
	upstream := &gatewayFallbackUpstream{}
	logs := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 4)}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewGatewayService(
		nil, groups, logs, nil, nil, nil, nil, nil, cfg, snapshot, nil,
		service.NewBillingService(cfg, nil), nil, billing, nil, upstream,
		service.NewDeferredService(nil, nil, time.Minute), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := &GatewayHandler{
		gatewayService: gateway, billingCacheService: billing, cfg: cfg, maxAccountSwitches: 4,
		apiKeyService:     service.NewAPIKeyService(nil, &gatewayFallbackUserRepo{user: user}, groups, nil, nil, nil, cfg),
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
	}
	return h, key, scheduler, upstream, logs.created
}

func TestGatewayGroupFallbackTextEndpointsRecordDestinationUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []struct {
		name         string
		primaryFails bool
		stream       bool
	}{
		{name: "empty_primary"},
		{name: "failed_primary", primaryFails: true},
		{name: "stream_failed_primary", primaryFails: true, stream: true},
	} {
		for _, endpoint := range []string{"messages", "responses", "chat/completions"} {
			t.Run(scenario.name+"/"+endpoint, func(t *testing.T) {
				h, key, scheduler, upstream, logs := newGatewayFallbackTestHandler(t)
				wantAccounts := []int64{41}
				if scenario.primaryFails {
					primaryAccount := *scheduler.accounts[0]
					primaryAccount.ID = 40
					primaryAccount.GroupIDs = []int64{11}
					primaryAccount.AccountGroups = []service.AccountGroup{{AccountID: 40, GroupID: 11}}
					scheduler.accounts = append(scheduler.accounts, &primaryAccount)
					upstream.failed = map[int64]bool{40: true}
					wantAccounts = []int64{40, 41}
				}
				body := `{"model":"claude-sonnet-4-5","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"stream":false}`
				call := h.Messages
				switch endpoint {
				case "responses":
					body = `{"model":"claude-sonnet-4-5","input":"hello","stream":false}`
					call = h.Responses
				case "chat/completions":
					call = h.ChatCompletions
				}
				if scenario.stream {
					body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
				}
				c, rec := newGatewayFallbackTestContext(key, endpoint, body)
				call(c)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "fallback ok")
				require.Contains(t, scheduler.visited, int64(11))
				require.Contains(t, scheduler.visited, int64(12))
				require.Equal(t, wantAccounts, upstream.accounts)
				require.Equal(t, int64(11), *key.GroupID, "the stored/cached key must not be rebound")
				select {
				case log := <-logs:
					require.Equal(t, key.ID, log.APIKeyID)
					require.Equal(t, int64(12), *log.GroupID)
					require.Equal(t, 2.0, log.RateMultiplier)
					require.Nil(t, log.SubscriptionID)
				case <-time.After(3 * time.Second):
					t.Fatal("fallback result did not record usage")
				}
			})
		}
	}
}

func newGatewayFallbackTestContext(key *service.APIKey, endpoint, body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, key.Group))
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 1})
	return c, rec
}

func TestGatewayGroupFallbackUnconfiguredKeepsPrimaryFailure(t *testing.T) {
	h, key, scheduler, upstream, _ := newGatewayFallbackTestHandler(t)
	key.FallbackGroupID = nil
	c, rec := newGatewayFallbackTestContext(key, "messages", `{"model":"claude-sonnet-4-5","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`)
	h.Messages(c)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.NotContains(t, scheduler.visited, int64(12))
	require.Empty(t, upstream.accounts)
}

func TestGatewayGroupFallbackPrimarySuccessKeepsRoutingAndBilling(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, endpoint := range []string{"messages", "responses", "chat/completions"} {
			name := "unconfigured/" + endpoint
			if configured {
				name = "configured/" + endpoint
			}
			t.Run(name, func(t *testing.T) {
				h, key, scheduler, upstream, logs := newGatewayFallbackTestHandler(t)
				if !configured {
					key.FallbackGroupID = nil
				}
				primary := scheduler.accounts[0]
				primary.GroupIDs = []int64{11}
				primary.AccountGroups = []service.AccountGroup{{AccountID: primary.ID, GroupID: 11}}
				body := `{"model":"claude-sonnet-4-5","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"stream":false}`
				call := h.Messages
				switch endpoint {
				case "responses":
					body = `{"model":"claude-sonnet-4-5","input":"hello","stream":false}`
					call = h.Responses
				case "chat/completions":
					call = h.ChatCompletions
				}
				c, rec := newGatewayFallbackTestContext(key, endpoint, body)
				call(c)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.NotContains(t, scheduler.visited, int64(12))
				require.Equal(t, []int64{41}, upstream.accounts)
				require.Equal(t, int64(11), *key.GroupID)
				select {
				case log := <-logs:
					require.Equal(t, int64(11), *log.GroupID)
					require.Equal(t, 1.0, log.RateMultiplier)
				case <-time.After(3 * time.Second):
					t.Fatal("primary result did not record usage")
				}
			})
		}
	}
}
