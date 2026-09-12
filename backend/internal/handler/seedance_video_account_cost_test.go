//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type seedanceCostRejectionConcurrencyCache struct {
	fakeConcurrencyCache
	acquires atomic.Int64
	releases atomic.Int64
}

func (c *seedanceCostRejectionConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	c.acquires.Add(1)
	return true, nil
}

func (c *seedanceCostRejectionConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.releases.Add(1)
	return nil
}

type seedanceCostRejectionTaskRepo struct {
	service.SeedanceVideoTaskRepository
	created   int
	released  int
	assigned  []int64
	bound     int
	assignErr error
}

func (r *seedanceCostRejectionTaskRepo) Create(context.Context, *service.SeedanceVideoPendingBilling) error {
	r.created++
	return nil
}

func (r *seedanceCostRejectionTaskRepo) MarkReleased(context.Context, string) error {
	r.released++
	return nil
}

func (r *seedanceCostRejectionTaskRepo) AssignAccount(_ context.Context, _ string, accountID int64, _ string, _ *service.SeedanceAccountCostSnapshot) error {
	r.assigned = append(r.assigned, accountID)
	return r.assignErr
}

func (r *seedanceCostRejectionTaskRepo) BindProviderTask(context.Context, string, string, string, time.Time) error {
	r.bound++
	return nil
}

type seedanceCostSuccessUpstream struct {
	openAIImagesFailoverHTTPUpstream
}

func (u *seedanceCostSuccessUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK,
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"job_id":"cost-failover-task","status":"queued"}`))}, nil
}

func seedanceCostHandlerAccount(id int64, priority int, tier string) service.Account {
	account := service.Account{ID: id, Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
		GroupIDs: []int64{9130}, AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: 9130}},
		Credentials: map[string]any{"api_key": "test-key", "video_provider": "fflink_v1"}}
	price := 0.03
	account.Extra = map[string]any{service.AccountModelCostPricingExtraKey: []service.ChannelModelPricing{{
		Models: []string{"seedance-2.0"}, BillingMode: service.BillingModeVideo,
		Intervals: []service.PricingInterval{{TierLabel: tier, PerRequestPrice: &price}},
	}}}
	return account
}

func runSeedanceAccountCostRequest(t *testing.T, accounts []service.Account, tasks *seedanceCostRejectionTaskRepo, maxSwitches int) (
	*httptest.ResponseRecorder, *seedanceCostRejectionConcurrencyCache, *seedanceCostSuccessUpstream, *service.OpenAIGatewayService,
) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	groupID := int64(9130)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformSeedance,
		Status: service.StatusActive, RateMultiplier: 1,
		VideoModelPrices: map[string]map[string]float64{"seedance-2.0": {"720p": 0.1}}}
	channelRepo := &openAIWSUsageHandlerChannelRepoStub{
		groupPlatforms: map[int64]string{groupID: service.PlatformSeedance},
		channels:       []service.Channel{{ID: 1, Status: service.StatusActive, GroupIDs: []int64{groupID}}},
	}
	channelService := service.NewChannelService(channelRepo, nil, nil, nil, nil)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	concurrencyCache := &seedanceCostRejectionConcurrencyCache{}
	concurrency := service.NewConcurrencyService(concurrencyCache)
	upstream := &seedanceCostSuccessUpstream{}
	gateway := service.NewOpenAIGatewayService(
		openAIImagesFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, concurrency, &service.BillingService{},
		nil, billingCache, upstream, nil, nil, nil, nil, channelService, nil, nil, nil,
	)
	gateway.SetSeedanceVideoTaskRepository(tasks)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.maxAccountSwitches = maxSwitches
	user := &service.User{ID: 9132, Balance: 100}
	apiKey := &service.APIKey{ID: 9133, UserID: user.ID, User: user, GroupID: &groupID, Group: group}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxkey.Group, group))
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(
		`{"model":"seedance-2.0","prompt":"waves","duration":5,"resolution":"720p"}`)).WithContext(ctx)
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: user.ID})

	h.SeedanceVideoGeneration(c)
	cancel()
	return recorder, concurrencyCache, upstream, gateway
}

func TestSeedanceVideoMissingAccountCostReleasesSelectedSlot(t *testing.T) {
	tasks := &seedanceCostRejectionTaskRepo{}
	recorder, concurrencyCache, upstream, gateway := runSeedanceAccountCostRequest(t,
		[]service.Account{seedanceCostHandlerAccount(9131, 0, "1080p")}, tasks, 3)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
	require.Equal(t, "seedance_account_cost_not_configured", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Empty(t, upstream.calls(), "missing cost must reject before contacting the video provider")
	require.Equal(t, int64(1), concurrencyCache.acquires.Load())
	require.Equal(t, int64(1), concurrencyCache.releases.Load(), "the scheduler's selected slot must be released exactly once")
	require.Equal(t, 1, tasks.created)
	require.Equal(t, 1, tasks.released, "the unsubmitted task must also be released")
	require.Zero(t, gateway.SnapshotOpenAIAccountSchedulerMetrics().RuntimeStatsAccountCount)
}

func TestSeedanceVideoMissingAccountCostSelectsNextAccount(t *testing.T) {
	tasks := &seedanceCostRejectionTaskRepo{}
	recorder, slots, upstream, _ := runSeedanceAccountCostRequest(t,
		[]service.Account{seedanceCostHandlerAccount(9131, 0, "1080p"), seedanceCostHandlerAccount(9134, 1, "720p")}, tasks, 3)
	require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
	require.Equal(t, []int64{9134}, upstream.calls(), "the account with missing cost must never contact its provider")
	require.Equal(t, []int64{9134}, tasks.assigned)
	require.Equal(t, int64(2), slots.acquires.Load())
	require.Equal(t, int64(2), slots.releases.Load())
	require.Equal(t, 1, tasks.created)
	require.Equal(t, 1, tasks.bound)
	require.Zero(t, tasks.released, "an accepted task must remain available for settlement")
}

func TestSeedanceVideoAllAccountCostsMissingRespectsSwitchLimit(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		accountCount, maxSwitches, wantAttempts int
	}{
		{"all_candidates_exhausted", 2, 3, 2},
		{"switch_budget_exhausted", 3, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := make([]service.Account, tc.accountCount)
			for i := range accounts {
				accounts[i] = seedanceCostHandlerAccount(int64(9131+i), i, "1080p")
			}
			tasks := &seedanceCostRejectionTaskRepo{}
			recorder, slots, upstream, gateway := runSeedanceAccountCostRequest(t, accounts, tasks, tc.maxSwitches)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
			require.Equal(t, "seedance_account_cost_not_configured", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
			require.Empty(t, upstream.calls())
			require.Empty(t, tasks.assigned)
			require.Equal(t, int64(tc.wantAttempts), slots.acquires.Load())
			require.Equal(t, slots.acquires.Load(), slots.releases.Load())
			require.Equal(t, 1, tasks.created)
			require.Equal(t, 1, tasks.released)
			require.Zero(t, gateway.SnapshotOpenAIAccountSchedulerMetrics().RuntimeStatsAccountCount)
		})
	}
}

func TestSeedanceVideoAccountAssignmentPersistenceErrorDoesNotRetry(t *testing.T) {
	tasks := &seedanceCostRejectionTaskRepo{assignErr: errors.New("account assignment storage unavailable")}
	recorder, slots, upstream, gateway := runSeedanceAccountCostRequest(t,
		[]service.Account{seedanceCostHandlerAccount(9131, 0, "720p"), seedanceCostHandlerAccount(9134, 1, "720p")}, tasks, 3)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
	require.Equal(t, "seedance_state_persistence_failed", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Equal(t, []int64{9131}, tasks.assigned)
	require.Empty(t, upstream.calls())
	require.Equal(t, int64(1), slots.acquires.Load())
	require.Equal(t, int64(1), slots.releases.Load())
	require.Equal(t, 1, tasks.created)
	require.Equal(t, 1, tasks.released)
	require.Zero(t, gateway.SnapshotOpenAIAccountSchedulerMetrics().RuntimeStatsAccountCount)
}
