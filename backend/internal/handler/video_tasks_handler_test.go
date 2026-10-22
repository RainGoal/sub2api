//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type videoTasksHandlerRepository struct {
	service.SeedanceVideoTaskRepository
	service.SeedanceVideoTaskReviewRepository
	pending                      *service.SeedanceVideoPendingBilling
	released, cancellationClaims int
	settleFailures               int
	settleDone                   chan struct{}
	metering                     json.RawMessage
}

func (r *videoTasksHandlerRepository) Create(_ context.Context, pending *service.SeedanceVideoPendingBilling) error {
	copy := *pending
	r.pending = &copy
	return nil
}

func (r *videoTasksHandlerRepository) AssignAccountWithExecution(_ context.Context, _ string, accountID int64, provider string, cost *service.SeedanceAccountCostSnapshot, execution *service.VideoTasksExecutionSnapshot, _ string) error {
	r.pending.AccountID, r.pending.ProviderID = accountID, provider
	r.pending.AccountCost, r.pending.ExecutionSnapshot = cost, execution
	return nil
}

func (r *videoTasksHandlerRepository) BindProviderTask(_ context.Context, _, taskID, status string, due time.Time) error {
	r.pending.TaskID, r.pending.UpstreamStatus, r.pending.NextPollAt = taskID, status, due
	return nil
}

func (r *videoTasksHandlerRepository) GetByProviderTask(_ context.Context, id string, userID, keyID int64) (*service.SeedanceVideoPendingBilling, error) {
	if r.pending == nil || id != r.pending.PublicID() || userID != r.pending.UserID || keyID != r.pending.APIKeyID {
		return nil, service.ErrSeedanceVideoTaskNotFound
	}
	copy := *r.pending
	return &copy, nil
}

func (r *videoTasksHandlerRepository) MarkReleased(context.Context, string) error {
	r.released++
	r.pending.SettlementStatus = service.SeedanceVideoSettlementReleased
	return nil
}

func (r *videoTasksHandlerRepository) MarkNeedsReview(_ context.Context, _, reason string, deadline time.Time) error {
	r.pending.SettlementStatus, r.pending.ReviewReason = service.SeedanceVideoSettlementNeedsReview, reason
	r.pending.ReviewDeadline = &deadline
	return nil
}

func (r *videoTasksHandlerRepository) ClaimCancellation(context.Context, string, int64, int64, time.Duration) (bool, error) {
	r.cancellationClaims++
	lease := time.Now().Add(30 * time.Second)
	r.pending.LeaseUntil = &lease
	r.pending.SettlementStatus = service.SeedanceVideoSettlementProcessing
	return true, nil
}

func (r *videoTasksHandlerRepository) MarkReleaseIntentWithLease(_ context.Context, _, status string, _ time.Time) error {
	r.pending.UpstreamStatus = status
	return nil
}

func (r *videoTasksHandlerRepository) MarkReleasedWithStatusWithLease(_ context.Context, _, status string, _ time.Time) error {
	r.released++
	r.pending.UpstreamStatus, r.pending.SettlementStatus = status, service.SeedanceVideoSettlementReleased
	r.pending.LeaseUntil = nil
	return nil
}

func (r *videoTasksHandlerRepository) ClaimSettlement(context.Context, string, int64, int64, time.Duration) (bool, error) {
	lease := time.Now().Add(30 * time.Second)
	r.pending.LeaseUntil, r.pending.SettlementStatus = &lease, service.SeedanceVideoSettlementProcessing
	return true, nil
}

func (r *videoTasksHandlerRepository) ClaimDue(_ context.Context, now time.Time, lease time.Duration, _ int) ([]*service.SeedanceVideoPendingBilling, error) {
	if r.pending == nil || r.pending.SettlementStatus == service.SeedanceVideoSettlementSettled || (r.pending.LeaseUntil != nil && r.pending.LeaseUntil.After(now)) {
		return nil, nil
	}
	until := now.Add(lease)
	r.pending.LeaseUntil, r.pending.SettlementStatus = &until, service.SeedanceVideoSettlementProcessing
	copy := *r.pending
	return []*service.SeedanceVideoPendingBilling{&copy}, nil
}

func (r *videoTasksHandlerRepository) GetVideoTaskMetering(context.Context, string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), r.metering...), nil
}

func (r *videoTasksHandlerRepository) SaveVideoTaskMeteringWithLease(_ context.Context, _ string, payload json.RawMessage, _ time.Time) error {
	if len(r.metering) > 0 && string(r.metering) != string(payload) {
		return errors.New("immutable metering conflict")
	}
	r.metering = append(json.RawMessage(nil), payload...)
	return nil
}

func (r *videoTasksHandlerRepository) MarkSettledWithLease(context.Context, string, float64, time.Time) error {
	if r.settleFailures > 0 {
		r.settleFailures--
		return errors.New("task state unavailable after billing commit")
	}
	r.pending.SettlementStatus, r.pending.LeaseUntil = service.SeedanceVideoSettlementSettled, nil
	if r.settleDone != nil {
		close(r.settleDone)
		r.settleDone = nil
	}
	return nil
}

type videoTasksHandlerBillingRepository struct {
	service.UsageBillingRepository
	reserved, released int
	held               float64
	commands           []*service.UsageBillingCommand
	applied            map[string]bool
}

func (r *videoTasksHandlerBillingRepository) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	copy := *command
	r.commands = append(r.commands, &copy)
	if r.applied == nil {
		r.applied = map[string]bool{}
	}
	if r.applied[command.RequestID] {
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}
	r.applied[command.RequestID] = true
	r.held -= command.BalanceHoldAmount
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func (r *videoTasksHandlerBillingRepository) ReserveBatchImageBalance(_ context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	r.reserved++
	r.held += cmd.HoldAmount
	return &service.BatchImageBalanceHoldResult{Applied: true}, nil
}

func (r *videoTasksHandlerBillingRepository) ReleaseBatchImageBalance(_ context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	r.released++
	r.held -= cmd.HoldAmount
	return &service.BatchImageBalanceHoldResult{Applied: true}, nil
}

type videoTasksHandlerUserRepository struct{ service.UserRepository }

func (videoTasksHandlerUserRepository) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id, Balance: 100}, nil
}

type videoTasksHandlerAPIKeyRepository struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r videoTasksHandlerAPIKeyRepository) GetByID(context.Context, int64) (*service.APIKey, error) {
	return r.key, nil
}

type videoTasksHandlerUpstreamCall struct {
	method, url, body string
	accountID         int64
}

type videoTasksHandlerUpstream struct {
	service.HTTPUpstream
	responses []*http.Response
	calls     []videoTasksHandlerUpstreamCall
	status    int
	body      string
	err       error
}

func (u *videoTasksHandlerUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	u.calls = append(u.calls, videoTasksHandlerUpstreamCall{req.Method, req.URL.String(), string(body), accountID})
	if len(u.responses) > 0 {
		response := u.responses[0]
		u.responses = u.responses[1:]
		return response, nil
	}
	if u.err != nil {
		return nil, u.err
	}
	return &http.Response{StatusCode: u.status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

type videoTasksHandlerHarness struct {
	handler  *OpenAIGatewayHandler
	cfg      *config.Config
	tasks    *videoTasksHandlerRepository
	billing  *videoTasksHandlerBillingRepository
	upstream *videoTasksHandlerUpstream
	accounts []service.Account
	key      *service.APIKey
	channel  *openAIWSUsageHandlerChannelRepoStub
}

func newVideoTasksHandlerHarness(t *testing.T, accountCounts ...int) *videoTasksHandlerHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	const model = "Seedance-2.0"
	groupID := int64(9240)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformSeedance,
		Status: service.StatusActive, RateMultiplier: 1}
	capability, ok := videoprovider.LookupVideoModelCapability(model, nil)
	require.True(t, ok)
	unitPrice := 0.25
	price := service.ChannelModelPricing{Models: []string{model}, Platform: service.PlatformSeedance,
		BillingMode: service.BillingModeVideo, VideoPricingVersion: service.VideoPricingVersion,
		VideoBillingUnit: service.VideoBillingPerVideo, PerRequestPrice: &unitPrice}
	accountCount := 2
	if len(accountCounts) > 0 {
		accountCount = accountCounts[0]
	}
	accounts := make([]service.Account, accountCount)
	for i := range accounts {
		accounts[i] = service.Account{ID: int64(9241 + i), Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i,
			GroupIDs: []int64{groupID}, AccountGroups: []service.AccountGroup{{AccountID: int64(9241 + i), GroupID: groupID}},
			Credentials: map[string]any{"api_key": "test-key", "base_url": "https://video.example/v1",
				"video_provider": string(videoprovider.ProviderVideoTasksJSONV1), "model_mapping": map[string]any{model: model}},
			Extra: map[string]any{service.VideoModelCapabilitiesExtra: []videoprovider.VideoModelCapability{capability},
				service.AccountModelCostPricingExtraKey: []service.ChannelModelPricing{price}}}
	}
	channelRepo := &openAIWSUsageHandlerChannelRepoStub{groupPlatforms: map[int64]string{groupID: service.PlatformSeedance},
		channels: []service.Channel{{ID: 2, Status: service.StatusActive, GroupIDs: []int64{groupID}, ModelPricing: []service.ChannelModelPricing{price}}}}
	cfg := &config.Config{}
	cfg.Gateway.VideoTasksJSONV1Enabled = true
	billingCache := service.NewBillingCacheService(nil, videoTasksHandlerUserRepository{}, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	concurrency := service.NewConcurrencyService(&seedanceCostRejectionConcurrencyCache{})
	upstream := &videoTasksHandlerUpstream{status: http.StatusOK, body: `{"id":"upstream-video-123","status":"queued"}`}
	billing := &videoTasksHandlerBillingRepository{}
	gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: accounts}, nil, billing,
		nil, nil, nil, nil, cfg, nil, concurrency, &service.BillingService{}, nil, billingCache, upstream,
		service.NewDeferredService(nil, nil, time.Minute), nil, nil, nil, service.NewChannelService(channelRepo, nil, nil, nil, nil), nil, nil, nil)
	tasks := &videoTasksHandlerRepository{}
	gateway.SetSeedanceVideoTaskRepository(tasks)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.maxAccountSwitches = 3
	user := &service.User{ID: 9243, Balance: 100}
	key := &service.APIKey{ID: 9244, UserID: user.ID, User: user, GroupID: &groupID, Group: group}
	return &videoTasksHandlerHarness{h, cfg, tasks, billing, upstream, accounts, key, channelRepo}
}

func (h *videoTasksHandlerHarness) request(method, id string, customBody ...string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx := context.WithValue(context.Background(), ctxkey.Group, h.key.Group)
	body := `{"model":"Seedance-2.0","prompt":"waves","duration":5,"resolution":"720p","referenceImages":["https://cdn.example/ref.png"]}`
	if len(customBody) > 0 {
		body = customBody[0]
	}
	c.Request = httptest.NewRequest(method, "/v1/videos/"+id, strings.NewReader(body)).WithContext(ctx)
	c.Params = gin.Params{{Key: "request_id", Value: id}}
	c.Set(string(middleware2.ContextKeyAPIKey), h.key)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: h.key.UserID})
	switch method {
	case http.MethodPost:
		h.handler.SeedanceVideoGeneration(c)
	case http.MethodGet:
		h.handler.SeedanceVideoStatus(c)
	case http.MethodDelete:
		h.handler.SeedanceVideoCancel(c)
	}
	return recorder
}

func TestVideoTasksHandlerUnknownSubmissionDoesNotFailoverOrReleaseHold(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{name: "transport_error", err: errors.New("connection reset after write")},
		{name: "server_error", status: http.StatusServiceUnavailable, body: `{"error":"retry later"}`},
		{name: "accepted_without_identity", status: http.StatusOK, body: `{"status":"queued"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t)
			h.upstream.status, h.upstream.body, h.upstream.err = tc.status, tc.body, tc.err
			response := h.request(http.MethodPost, "")
			require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
			require.Equal(t, "video_submission_unknown", gjson.GetBytes(response.Body.Bytes(), "error.type").String())
			require.Len(t, h.upstream.calls, 1, "an ambiguous submission must never select another account")
			require.Equal(t, 1, h.billing.reserved)
			require.Zero(t, h.billing.released, "a possible upstream task still owns its balance hold")
			require.InDelta(t, 0.25, h.billing.held, 1e-8)
			require.Zero(t, h.tasks.released)
			require.Equal(t, service.SeedanceVideoSettlementNeedsReview, h.tasks.pending.SettlementStatus)
			require.Equal(t, "submission_outcome_unknown", h.tasks.pending.ReviewReason)
			require.NotNil(t, h.tasks.pending.ReviewDeadline)
			require.Equal(t, h.tasks.pending.PublicID(), response.Header().Get("X-Video-Task-ID"))
			require.Regexp(t, `^vid_[0-9a-f]{32}$`, response.Header().Get("X-Video-Task-ID"))
		})
	}
}

func TestVideoTasksHandlerFreezesTaskRoutingAndUsesPublicIdentity(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	created := h.request(http.MethodPost, "")
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	id := gjson.GetBytes(created.Body.Bytes(), "id").String()
	require.Regexp(t, `^vid_[0-9a-f]{32}$`, id)
	require.Equal(t, h.tasks.pending.PublicID(), id)
	require.Equal(t, "upstream-video-123", h.tasks.pending.UpstreamID())
	require.NotContains(t, created.Body.String(), "upstream-video-123")
	require.Len(t, h.upstream.calls, 1)
	require.Equal(t, "waves", gjson.Get(h.upstream.calls[0].body, "prompt").String())
	require.Equal(t, "https://cdn.example/ref.png", gjson.Get(h.upstream.calls[0].body, "referenceImages.0").String())
	h.accounts[0].Credentials["base_url"] = "https://changed.example/other"
	h.accounts[0].Credentials["video_provider"] = "fflink_v1"
	h.accounts[0].Extra[service.VideoModelCapabilitiesExtra] = nil
	h.cfg.Gateway.VideoTasksJSONV1Enabled = false
	h.upstream.body = `{"id":"upstream-video-123","status":"running"}`
	status := h.request(http.MethodGet, id)
	require.Equal(t, http.StatusOK, status.Code, status.Body.String())
	require.Equal(t, id, gjson.GetBytes(status.Body.Bytes(), "id").String())
	require.Equal(t, "in_progress", gjson.GetBytes(status.Body.Bytes(), "status").String())
	require.Len(t, h.upstream.calls, 2)
	require.Equal(t, "https://video.example/v1/videos/upstream-video-123", h.upstream.calls[1].url)
	require.Equal(t, h.upstream.calls[0].accountID, h.upstream.calls[1].accountID)
	require.Zero(t, h.billing.released)
}

func TestVideoTasksHandlerPreservesNormalizedResolutionAlias(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	response := h.request(http.MethodPost, "", `{"model":"Seedance-2.0","prompt":"waves","size":"720P","ratio":"16:9"}`)
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	require.Len(t, h.upstream.calls, 1)
	require.Equal(t, "720p", gjson.Get(h.upstream.calls[0].body, "resolution").String())
	require.Equal(t, "16:9", gjson.Get(h.upstream.calls[0].body, "ratio").String())
}

func TestVideoTasksHandlerBBLabuCancellationDoesNotClaimOrContactUpstream(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	created := h.request(http.MethodPost, "")
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	id := gjson.GetBytes(created.Body.Bytes(), "id").String()
	response := h.request(http.MethodDelete, id)
	require.Equal(t, http.StatusNotImplemented, response.Code, response.Body.String())
	require.Equal(t, "operation_not_supported", gjson.GetBytes(response.Body.Bytes(), "error.type").String())
	require.Zero(t, h.tasks.cancellationClaims, "capability rejection must happen before acquiring a cancellation lease")
	require.Len(t, h.upstream.calls, 1, "unsupported cancellation must not send DELETE")
	require.Zero(t, h.billing.released)
	require.InDelta(t, 0.25, h.billing.held, 1e-8)
}

func TestVideoTasksHandlerSupportedCancellationFinalizesByPublicIdentity(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	created := h.request(http.MethodPost, "")
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	id := gjson.GetBytes(created.Body.Bytes(), "id").String()
	// A custom supplier may explicitly implement the protocol's cancellation operation.
	h.tasks.pending.ExecutionSnapshot.SupportsCancel = true
	h.tasks.pending.ExecutionSnapshot.Capability.SupportsCancel = true
	h.upstream.body = `{"id":"upstream-video-123","status":"canceled"}`
	response := h.request(http.MethodDelete, id)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, id, gjson.GetBytes(response.Body.Bytes(), "id").String())
	require.Equal(t, "canceled", gjson.GetBytes(response.Body.Bytes(), "status").String())
	require.Equal(t, 1, h.tasks.cancellationClaims)
	require.Len(t, h.upstream.calls, 2)
	require.Equal(t, http.MethodDelete, h.upstream.calls[1].method)
	require.Equal(t, "https://video.example/v1/videos/upstream-video-123", h.upstream.calls[1].url)
	require.Equal(t, 1, h.billing.released)
	require.Zero(t, h.billing.held)
	require.Equal(t, service.SeedanceVideoSettlementReleased, h.tasks.pending.SettlementStatus)
}

func TestVideoTasksHandlerRejectsUpstreamIdentityAndDifferentOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*videoTasksHandlerHarness, *string)
	}{
		{"upstream_identity", func(h *videoTasksHandlerHarness, id *string) { *id = h.tasks.pending.UpstreamID() }},
		{"different_user", func(h *videoTasksHandlerHarness, _ *string) { h.key.UserID++ }},
		{"different_key", func(h *videoTasksHandlerHarness, _ *string) { h.key.ID++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t)
			created := h.request(http.MethodPost, "")
			require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
			id := gjson.GetBytes(created.Body.Bytes(), "id").String()
			tc.mutate(h, &id)
			response := h.request(http.MethodGet, id)
			require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
			require.Len(t, h.upstream.calls, 1)
		})
	}
}

func TestVideoTasksHandlerFiltersAllLocalCandidatesBeforeSubmitting(t *testing.T) {
	for _, rejection := range []string{"capability", "purchase_price"} {
		t.Run(rejection, func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t, 6)
			for i := 0; i < len(h.accounts)-1; i++ {
				if rejection == "capability" {
					capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
					require.True(t, ok)
					capability.Resolutions, capability.DefaultResolution = []string{"480p"}, "480p"
					h.accounts[i].Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
				} else {
					delete(h.accounts[i].Extra, service.AccountModelCostPricingExtraKey)
				}
			}
			created := h.request(http.MethodPost, "")
			require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
			require.Len(t, h.upstream.calls, 1, "local rejections must never create provider jobs")
			require.Equal(t, h.accounts[5].ID, h.upstream.calls[0].accountID)
			require.Equal(t, h.accounts[5].ID, h.tasks.pending.AccountID)
			require.Equal(t, 1, h.billing.reserved)
			require.Zero(t, h.billing.released)
			require.InDelta(t, 0.25, h.billing.held, 1e-8)
		})
	}
}

func TestVideoTasksHandlerSkipsAccountWithoutTrustedSalesMetering(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	price := &h.channel.channels[0].ModelPricing[0]
	price.VideoBillingUnit = service.VideoBillingPerSecond
	price.VideoQuantityPolicy = service.VideoQuantityOutputSeconds
	capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
	require.True(t, ok)
	capability.MeteringTrusted = false
	h.accounts[0].Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
	created := h.request(http.MethodPost, "")
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	require.Len(t, h.upstream.calls, 1)
	require.Equal(t, h.accounts[1].ID, h.upstream.calls[0].accountID)
	require.Equal(t, h.accounts[1].ID, h.tasks.pending.AccountID)
	require.Equal(t, service.VideoBillingPerSecond, h.tasks.pending.SalesSnapshot.BillingUnit)
	require.Equal(t, service.VideoBillingPerVideo, h.tasks.pending.AccountCost.BillingUnit)
	require.Equal(t, 1, h.billing.reserved)
	require.Zero(t, h.billing.released)
}

func TestVideoTasksHandlerAndRecoveryShareBillingIdentityAfterStateFailure(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	created := h.request(http.MethodPost, "")
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	id := gjson.GetBytes(created.Body.Bytes(), "id").String()
	h.tasks.settleFailures = 1
	h.upstream.body = `{"id":"upstream-video-123","status":"completed","duration":5}`
	response := h.request(http.MethodGet, id)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, h.billing.commands, 1, "the HTTP poll must have committed its bill before the state write failed")
	require.Equal(t, service.StableSeedanceVideoBillingRequestID(id), h.billing.commands[0].RequestID)
	require.Equal(t, service.SeedanceVideoSettlementProcessing, h.tasks.pending.SettlementStatus)
	originalMetering := string(h.tasks.metering)
	// A later completion can omit the duration that was frozen before payment.
	h.upstream.body = `{"id":"upstream-video-123","status":"completed"}`
	// Simulate the abandoned HTTP lease expiring before the recovery worker claims it.
	expired := time.Now().Add(-time.Second)
	h.tasks.pending.LeaseUntil = &expired
	settled := make(chan struct{})
	h.tasks.settleDone = settled
	recovery := service.NewSeedanceVideoRecoveryService(h.handler.gatewayService,
		videoTasksHandlerAPIKeyRepository{key: h.key}, openAIImagesFailoverAccountRepo{accounts: h.accounts}, nil, h.handler.apiKeyService)
	recovery.Start()
	t.Cleanup(recovery.Stop)
	select {
	case <-settled:
	case <-time.After(8 * time.Second):
		recovery.Stop()
		t.Fatal("recovery did not finish the already billed task")
	}
	recovery.Stop()
	require.Len(t, h.billing.commands, 2)
	require.Equal(t, h.billing.commands[0].RequestID, h.billing.commands[1].RequestID)
	require.Equal(t, h.billing.commands[0].RequestFingerprint, h.billing.commands[1].RequestFingerprint)
	require.Len(t, h.billing.applied, 1, "the recovered task must not incur a second charge")
	require.Zero(t, h.billing.held)
	require.Equal(t, service.SeedanceVideoSettlementSettled, h.tasks.pending.SettlementStatus)
	require.JSONEq(t, originalMetering, string(h.tasks.metering))
}

func TestVideoTasksHandlerReusesFrozenMeteringBeforeCompletionBuild(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	h.channel.channels[0].ModelPricing[0].VideoBillingUnit = service.VideoBillingPerSecond
	h.channel.channels[0].ModelPricing[0].VideoQuantityPolicy = service.VideoQuantityOutputSeconds
	created := h.request(http.MethodPost, "")
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	id := gjson.GetBytes(created.Body.Bytes(), "id").String()
	h.tasks.settleFailures = 1
	h.upstream.body = `{"id":"upstream-video-123","status":"completed","duration":5}`
	response := h.request(http.MethodGet, id)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, h.billing.commands, 1)
	original := string(h.tasks.metering)
	// Model the retry scheduler reopening the row after the failed state write.
	h.tasks.pending.SettlementStatus, h.tasks.pending.LeaseUntil = service.SeedanceVideoSettlementPending, nil
	h.upstream.body = `{"id":"upstream-video-123","status":"completed"}`
	response = h.request(http.MethodGet, id)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, h.billing.commands, 2)
	require.Equal(t, h.billing.commands[0].RequestFingerprint, h.billing.commands[1].RequestFingerprint)
	require.Len(t, h.billing.applied, 1)
	require.Equal(t, service.SeedanceVideoSettlementSettled, h.tasks.pending.SettlementStatus)
	require.JSONEq(t, original, string(h.tasks.metering))
}
