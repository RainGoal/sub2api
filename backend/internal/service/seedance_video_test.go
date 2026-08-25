package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type seedanceVideoUpstream struct {
	HTTPUpstream
	requests  []*http.Request
	bodies    [][]byte
	responses []*http.Response
}

func (u *seedanceVideoUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}
	u.requests = append(u.requests, req)
	u.bodies = append(u.bodies, body)
	if len(u.responses) == 0 {
		return nil, errors.New("unexpected upstream request")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func seedanceVideoTestAccount() *Account {
	return &Account{
		ID: 20, Platform: PlatformSeedance, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":                 "seedance-secret",
			"base_url":                "https://api.bblabu.ai/v1",
			"header_override_enabled": true,
			"header_overrides":        map[string]any{"X-Tenant": "tenant-1"},
		},
	}
}

func mustPrepareSeedanceVideoRequest(t *testing.T, body []byte) *SeedanceVideoCreateRequest {
	t.Helper()
	request, _, _, err := PrepareSeedanceVideoRequest(body)
	require.NoError(t, err)
	return request
}

func TestForwardSeedanceVideoCreateUsesBblabuCanonicalEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"Seedance-2.0","prompt":"waves","duration":10,"resolution":"720p"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"upstream-create"},
		},
		Body: io.NopCloser(strings.NewReader(`{"task_id":"task-123","status":"queued"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(context.Background(), c, seedanceVideoTestAccount(), string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointCreate, "", mustPrepareSeedanceVideoRequest(t, body))

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, http.MethodPost, upstream.requests[0].Method)
	require.Equal(t, "https://api.bblabu.ai/v1/videos", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer seedance-secret", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "tenant-1", getHeaderRaw(upstream.requests[0].Header, "x-tenant"))
	require.JSONEq(t, `{"model":"Seedance-2.0","prompt":"waves","duration":10,"resolution":"720p","ratio":"16:9"}`, string(upstream.bodies[0]))
	require.Equal(t, "task-123", result.ResponseID)
	require.Equal(t, "upstream-create", result.RequestID)
	require.Equal(t, PlatformSeedance, result.VideoProvider)
	require.Zero(t, result.VideoCount)
	require.Empty(t, recorder.Body.String())
	require.NoError(t, svc.CommitSeedanceVideoResponse(c, result))
	require.JSONEq(t, `{"task_id":"task-123","status":"queued"}`, recorder.Body.String())
}

func TestForwardSeedanceVideoUsesFFLinkCreateAndCancelContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	body := []byte(`{"model":"seedance-2.0-fast","prompt":"waves","duration":4,"resolution":"720p","aspect_ratio":"21:9"}`)
	upstream := &seedanceVideoUpstream{responses: []*http.Response{
		{
			StatusCode: http.StatusAccepted,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"job_id":"vidjob-1","status":"pending"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"job_id":"vidjob-1","status":"canceled"}`)),
		},
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	createResult, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account, string(videoprovider.ProviderFFLinkV1),
		SeedanceVideoEndpointCreate, "", mustPrepareSeedanceVideoRequest(t, body),
	)
	require.NoError(t, err)
	require.Equal(t, "vidjob-1", createResult.ResponseID)
	require.Equal(t, "https://api.fflink.top/v1/videos/generations", upstream.requests[0].URL.String())
	require.Equal(t, http.MethodPost, upstream.requests[0].Method)
	require.Equal(t, "respond-async", upstream.requests[0].Header.Get("Prefer"))
	require.JSONEq(t, `{"model":"seedance-2.0-fast","prompt":"waves","duration":4,"resolution":"720p","aspect_ratio":"21:9","audio":false}`, string(upstream.bodies[0]))

	cancelResult, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account, string(videoprovider.ProviderFFLinkV1),
		SeedanceVideoEndpointCancel, "vidjob-1", nil,
	)
	require.NoError(t, err)
	require.Equal(t, "vidjob-1", cancelResult.ResponseID)
	require.Equal(t, "canceled", cancelResult.TaskStatus)
	require.Equal(t, "https://api.fflink.top/v1/videos/jobs/vidjob-1", upstream.requests[1].URL.String())
	require.Equal(t, http.MethodDelete, upstream.requests[1].Method)
}

func TestForwardSeedanceVideoStatusParsesCompletedBillingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task-123", nil)
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"task-123","status":"completed","model":"seedance-2.5",
			"resolution":"720p","duration":30,"usage":{"reference_video_input_seconds":12}
		}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(context.Background(), c, seedanceVideoTestAccount(), string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointStatus, "task-123", nil)

	require.NoError(t, err)
	require.Equal(t, "https://api.bblabu.ai/v1/videos/task-123", upstream.requests[0].URL.String())
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Empty(t, upstream.bodies[0])
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, "seedance-2.5", result.BillingModel)
	require.Equal(t, "720p", result.VideoResolution)
	require.Equal(t, 30, result.VideoDurationSeconds)
	require.Equal(t, 12, result.VideoReferenceInputSeconds)
}

func TestForwardSeedanceVideoCreateRejectsSuccessfulResponseWithoutTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{}`))
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"queued"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	request := &SeedanceVideoCreateRequest{RequestedModel: "Seedance-2.0", Model: videoprovider.ModelSeedance20, Prompt: "waves", Duration: 10, Resolution: "720p", AspectRatio: "16:9", Raw: map[string]any{}}
	result, err := svc.ForwardSeedanceVideo(context.Background(), c, seedanceVideoTestAccount(), string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointCreate, "", request)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, recorder.Result().Header.Get("Content-Type") != "" || recorder.Body.Len() > 0)
}

func TestForwardSeedanceVideoContentKeepsTaskAffinityProtocolAndRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task-123/content", nil)
	c.Request.Header.Set("Range", "bytes=0-3")
	upstream := &seedanceVideoUpstream{responses: []*http.Response{
		{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"id":"task-123","status":"completed"}`)),
		},
		{
			StatusCode: http.StatusPartialContent,
			Header: http.Header{
				"Content-Type":  []string{"video/mp4"},
				"Content-Range": []string{"bytes 0-3/8"},
			},
			ContentLength: 4,
			Body:          io.NopCloser(strings.NewReader("DATA")),
		},
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(context.Background(), c, seedanceVideoTestAccount(), string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointContent, "task-123", nil)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://api.bblabu.ai/v1/videos/task-123", upstream.requests[0].URL.String())
	require.Equal(t, "https://api.bblabu.ai/v1/videos/task-123/content", upstream.requests[1].URL.String())
	require.Equal(t, "bytes=0-3", upstream.requests[1].Header.Get("Range"))
	require.Equal(t, "Bearer seedance-secret", upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, http.StatusPartialContent, recorder.Code)
	require.Equal(t, "DATA", recorder.Body.String())
	require.Equal(t, 1, result.VideoCount)
}

func TestSeedanceVideoURLRejectsUnsafeTaskIDAndQueryBaseURL(t *testing.T) {
	driver, err := videoprovider.Resolve(string(videoprovider.ProviderBBLabuV1))
	require.NoError(t, err)
	_, err = driver.BuildRequest(context.Background(), videoprovider.RequestParams{
		BaseURL: "https://api.example/v1?tenant=1", APIKey: "test-key", Operation: videoprovider.OperationCreate,
		CreateRequest: &videoprovider.CreateRequest{RequestedModel: videoprovider.ModelSeedance20, Model: videoprovider.ModelSeedance20, Prompt: "waves", Duration: 10, Resolution: "720p", AspectRatio: "16:9", Raw: map[string]any{}},
	})
	require.Error(t, err)
	_, err = driver.BuildRequest(context.Background(), videoprovider.RequestParams{
		BaseURL: "https://api.example/v1", APIKey: "test-key", Operation: videoprovider.OperationStatus, TaskID: "bad\nvalue",
	})
	require.Error(t, err)
	_, err = driver.BuildRequest(context.Background(), videoprovider.RequestParams{
		BaseURL: "http://api.example/v1", APIKey: "test-key", Operation: videoprovider.OperationCreate,
		CreateRequest: &videoprovider.CreateRequest{RequestedModel: videoprovider.ModelSeedance20, Model: videoprovider.ModelSeedance20, Prompt: "waves", Duration: 10, Resolution: "720p", AspectRatio: "16:9", Raw: map[string]any{}},
	})
	require.Error(t, err)
}

type seedanceVideoCache struct {
	GatewayCache
	sessionAccountID int64
	payload          []byte
	claimed          bool
	setSessionErr    error
}

type seedanceVideoTaskMemoryRepo struct {
	tasks   map[string]*SeedanceVideoPendingBilling
	bindErr error
}

func (r *seedanceVideoTaskMemoryRepo) Create(_ context.Context, pending *SeedanceVideoPendingBilling) error {
	if r.tasks == nil {
		r.tasks = make(map[string]*SeedanceVideoPendingBilling)
	}
	copy := *pending
	copy.SettlementStatus = SeedanceVideoSettlementPending
	r.tasks[pending.StateID] = &copy
	return nil
}

func (r *seedanceVideoTaskMemoryRepo) AssignAccount(_ context.Context, stateID string, accountID int64, providerID string) error {
	pending := r.tasks[stateID]
	if pending == nil {
		return ErrSeedanceVideoTaskNotFound
	}
	pending.AccountID = accountID
	pending.ProviderID = providerID
	return nil
}

func (r *seedanceVideoTaskMemoryRepo) BindProviderTask(_ context.Context, stateID, taskID, status string, dueAt time.Time) error {
	if r.bindErr != nil {
		return r.bindErr
	}
	pending := r.tasks[stateID]
	if pending == nil {
		return ErrSeedanceVideoTaskNotFound
	}
	pending.TaskID = taskID
	pending.UpstreamStatus = status
	pending.NextPollAt = dueAt
	return nil
}

func (r *seedanceVideoTaskMemoryRepo) GetByProviderTask(_ context.Context, taskID string, userID, apiKeyID int64) (*SeedanceVideoPendingBilling, error) {
	for _, pending := range r.tasks {
		if pending.TaskID == taskID && pending.UserID == userID && pending.APIKeyID == apiKeyID {
			copy := *pending
			return &copy, nil
		}
	}
	return nil, ErrSeedanceVideoTaskNotFound
}

func (r *seedanceVideoTaskMemoryRepo) ClaimDue(_ context.Context, now time.Time, lease time.Duration, limit int) ([]*SeedanceVideoPendingBilling, error) {
	tasks := make([]*SeedanceVideoPendingBilling, 0, limit)
	for _, pending := range r.tasks {
		if len(tasks) >= limit || pending.SettlementStatus != SeedanceVideoSettlementPending || pending.NextPollAt.After(now) {
			continue
		}
		pending.SettlementStatus = SeedanceVideoSettlementProcessing
		leaseUntil := now.Add(lease)
		pending.LeaseUntil = &leaseUntil
		copy := *pending
		tasks = append(tasks, &copy)
	}
	return tasks, nil
}

func (r *seedanceVideoTaskMemoryRepo) ClaimSettlement(_ context.Context, taskID string, userID, apiKeyID int64, _ time.Duration) (bool, error) {
	for _, pending := range r.tasks {
		if pending.TaskID == taskID && pending.UserID == userID && pending.APIKeyID == apiKeyID {
			if pending.SettlementStatus != SeedanceVideoSettlementPending {
				return false, nil
			}
			pending.SettlementStatus = SeedanceVideoSettlementProcessing
			return true, nil
		}
	}
	return false, ErrSeedanceVideoTaskNotFound
}

func (r *seedanceVideoTaskMemoryRepo) Reschedule(_ context.Context, stateID, status string, dueAt time.Time, lastError string) error {
	pending := r.tasks[stateID]
	if pending == nil {
		return ErrSeedanceVideoTaskNotFound
	}
	pending.UpstreamStatus = status
	pending.SettlementStatus = SeedanceVideoSettlementPending
	pending.NextPollAt = dueAt
	pending.LastError = lastError
	return nil
}

func (r *seedanceVideoTaskMemoryRepo) MarkSettled(_ context.Context, stateID string, _ float64) error {
	pending := r.tasks[stateID]
	if pending == nil {
		return ErrSeedanceVideoTaskNotFound
	}
	pending.SettlementStatus = SeedanceVideoSettlementSettled
	return nil
}

func (r *seedanceVideoTaskMemoryRepo) MarkReleased(_ context.Context, stateID string) error {
	pending := r.tasks[stateID]
	if pending == nil {
		return ErrSeedanceVideoTaskNotFound
	}
	pending.SettlementStatus = SeedanceVideoSettlementReleased
	return nil
}

type seedanceVideoAPIKeyRepo struct {
	APIKeyRepository
	apiKey *APIKey
}

type seedanceVideoBillingRepo struct {
	commands []*UsageBillingCommand
	releases []*BatchImageBalanceHoldCommand
	seen     map[string]struct{}
}

func (r *seedanceVideoBillingRepo) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	cmd.Normalize()
	r.commands = append(r.commands, cmd)
	if _, exists := r.seen[cmd.RequestID]; exists {
		return &UsageBillingApplyResult{Applied: false}, nil
	}
	r.seen[cmd.RequestID] = struct{}{}
	return &UsageBillingApplyResult{Applied: true}, nil
}

func (r *seedanceVideoBillingRepo) ReserveBatchImageBalance(_ context.Context, _ *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	return &BatchImageBalanceHoldResult{Applied: true}, nil
}

func (r *seedanceVideoBillingRepo) CaptureBatchImageBalance(_ context.Context, _ *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	return &BatchImageBalanceHoldResult{Applied: true}, nil
}

func (r *seedanceVideoBillingRepo) ReleaseBatchImageBalance(_ context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	r.releases = append(r.releases, cmd)
	return &BatchImageBalanceHoldResult{Applied: true}, nil
}

func (r seedanceVideoAPIKeyRepo) GetByID(_ context.Context, _ int64) (*APIKey, error) {
	return r.apiKey, nil
}

func (c *seedanceVideoCache) SetSessionAccountID(_ context.Context, _ int64, _ string, accountID int64, _ time.Duration) error {
	if c.setSessionErr != nil {
		return c.setSessionErr
	}
	c.sessionAccountID = accountID
	return nil
}

func (c *seedanceVideoCache) GetSessionAccountID(_ context.Context, _ int64, _ string) (int64, error) {
	if c.sessionAccountID <= 0 {
		return 0, ErrStickySessionNotFound
	}
	return c.sessionAccountID, nil
}

func (c *seedanceVideoCache) SetGrokVideoPendingBilling(_ context.Context, _ string, payload []byte, _ time.Duration) error {
	c.payload = append([]byte(nil), payload...)
	return nil
}

func (c *seedanceVideoCache) GetGrokVideoPendingBilling(_ context.Context, _ string) ([]byte, error) {
	return append([]byte(nil), c.payload...), nil
}

func (c *seedanceVideoCache) ClaimGrokVideoBilled(_ context.Context, _ string, _ time.Duration) (bool, error) {
	if c.claimed {
		return false, nil
	}
	c.claimed = true
	return true, nil
}

func (c *seedanceVideoCache) ReleaseGrokVideoBilled(_ context.Context, _ string) error {
	c.claimed = false
	return nil
}

func storeSeedanceTestPending(t *testing.T, svc *OpenAIGatewayService, taskID string, userID, apiKeyID int64, pending SeedanceVideoPendingBilling) *SeedanceVideoPendingBilling {
	t.Helper()
	if pending.StateID == "" {
		pending.StateID = "state:" + taskID
	}
	if pending.HoldID == "" {
		pending.HoldID = "seedance:hold:" + taskID
	}
	pending.UserID = userID
	pending.APIKeyID = apiKeyID
	if pending.CreatedAt == "" {
		pending.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if pending.ProviderID == "" {
		pending.ProviderID = string(videoprovider.ProviderBBLabuV1)
	}
	require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), &pending))
	require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), &pending, pending.AccountID, pending.ProviderID))
	require.NoError(t, svc.StoreSeedanceVideoPendingBilling(context.Background(), taskID, userID, apiKeyID, pending))
	stored, err := svc.LoadSeedanceVideoPendingBilling(context.Background(), taskID, userID, apiKeyID)
	require.NoError(t, err)
	return stored
}

func TestSeedanceVideoTaskBindingAndBillingClaimAreStable(t *testing.T) {
	cache := &seedanceVideoCache{}
	repo := &seedanceVideoTaskMemoryRepo{}
	svc := &OpenAIGatewayService{cache: cache, seedanceVideoTaskRepo: repo}
	groupID := int64(9)
	ctx := context.Background()

	require.NoError(t, svc.BindSeedanceVideoTaskAccount(ctx, &groupID, "task-123", 7, 8, 20))
	accountID, err := svc.ResolveSeedanceVideoTaskAccount(ctx, &groupID, "task-123", 7, 8)
	require.NoError(t, err)
	require.Equal(t, int64(20), accountID)

	pending := SeedanceVideoPendingBilling{
		AccountID: 20,
		Model:     "Seedance-2.5", Resolution: "720p", DurationSeconds: 30,
	}
	loaded := storeSeedanceTestPending(t, svc, "task-123", 7, 8, pending)
	require.Equal(t, pending.Model, loaded.Model)
	require.Equal(t, pending.DurationSeconds, loaded.DurationSeconds)

	claimed, err := svc.ClaimSeedanceVideoBilling(ctx, "task-123", 7, 8)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = svc.ClaimSeedanceVideoBilling(ctx, "task-123", 7, 8)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, svc.ReleaseSeedanceVideoBilling(ctx, "task-123", 7, 8))
	claimed, err = svc.ClaimSeedanceVideoBilling(ctx, "task-123", 7, 8)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, "seedance-video:task-123", StableSeedanceVideoBillingRequestID("task-123"))
}

func TestResolveSeedanceVideoTaskAccountRecoversFromPendingBilling(t *testing.T) {
	cache := &seedanceVideoCache{}
	svc := &OpenAIGatewayService{cache: cache, seedanceVideoTaskRepo: &seedanceVideoTaskMemoryRepo{}}
	ctx := context.Background()
	groupID := int64(9)

	storeSeedanceTestPending(t, svc, "task-recover", 7, 8, SeedanceVideoPendingBilling{
		AccountID: 20, Model: "Seedance-2.5", Resolution: "720p", DurationSeconds: 30,
	})
	require.Zero(t, cache.sessionAccountID)

	accountID, err := svc.ResolveSeedanceVideoTaskAccount(ctx, &groupID, "task-recover", 7, 8)
	require.NoError(t, err)
	require.Equal(t, int64(20), accountID)
	require.Equal(t, int64(20), cache.sessionAccountID)
}

func TestResolveSeedanceVideoTaskAccountUsesDatabaseWithoutCache(t *testing.T) {
	repo := &seedanceVideoTaskMemoryRepo{}
	svc := &OpenAIGatewayService{seedanceVideoTaskRepo: repo}
	groupID := int64(9)
	storeSeedanceTestPending(t, svc, "task-no-cache", 7, 8, SeedanceVideoPendingBilling{
		AccountID: 20, Model: "Seedance-2.0", Resolution: "720p", DurationSeconds: 10,
	})

	accountID, err := svc.ResolveSeedanceVideoTaskAccount(context.Background(), &groupID, "task-no-cache", 7, 8)
	require.NoError(t, err)
	require.Equal(t, int64(20), accountID)
}

func TestSelectSeedanceVideoTaskAccountUsesPersistedAccount(t *testing.T) {
	groupID := int64(9)
	account := seedanceVideoTestAccount()
	account.Status = StatusActive
	account.Schedulable = true
	account.GroupIDs = []int64{groupID}
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*account}},
		cfg:         &config.Config{},
	}

	selection, decision, err := svc.SelectSeedanceVideoTaskAccount(context.Background(), &groupID, account.ID)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, account.ID, selection.Account.ID)
	require.Equal(t, "seedance_task_binding", decision.Layer)
	require.NotNil(t, selection.ReleaseFunc)
	selection.ReleaseFunc()

	otherGroupID := int64(10)
	selection, _, err = svc.SelectSeedanceVideoTaskAccount(context.Background(), &otherGroupID, account.ID)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
}

func TestStoreSeedanceVideoPendingBillingFailsClosedWhenDatabaseBindFails(t *testing.T) {
	repo := &seedanceVideoTaskMemoryRepo{bindErr: errors.New("database unavailable")}
	svc := &OpenAIGatewayService{cache: &seedanceVideoCache{}, seedanceVideoTaskRepo: repo}
	pending := SeedanceVideoPendingBilling{
		StateID: "state:task-fail", HoldID: "seedance:hold:task-fail", UserID: 7, APIKeyID: 8,
		AccountID: 20, Model: "Seedance-2.0", Resolution: "720p", DurationSeconds: 10,
	}
	require.NoError(t, svc.BeginSeedanceVideoTask(context.Background(), &pending))
	require.NoError(t, svc.AssignSeedanceVideoTaskAccount(context.Background(), &pending, pending.AccountID, pending.ProviderID))
	err := svc.StoreSeedanceVideoPendingBilling(context.Background(), "task-fail", 7, 8, pending)
	require.ErrorContains(t, err, "database unavailable")
}

func TestResolveSeedanceVideoTaskAccountUsesPendingWhenStickyWriteFails(t *testing.T) {
	cache := &seedanceVideoCache{}
	svc := &OpenAIGatewayService{cache: cache, seedanceVideoTaskRepo: &seedanceVideoTaskMemoryRepo{}}
	ctx := context.Background()
	groupID := int64(9)
	storeSeedanceTestPending(t, svc, "task-sticky", 7, 8, SeedanceVideoPendingBilling{
		AccountID: 20, Model: "Seedance-2.0", Resolution: "720p", DurationSeconds: 10,
	})
	cache.setSessionErr = errors.New("sticky unavailable")
	require.Error(t, svc.BindSeedanceVideoTaskAccount(ctx, &groupID, "task-sticky", 7, 8, 20))
	accountID, err := svc.ResolveSeedanceVideoTaskAccount(ctx, &groupID, "task-sticky", 7, 8)
	require.NoError(t, err)
	require.Equal(t, int64(20), accountID)
}

func TestBuildSeedanceVideoCompletionBillingIncludesReferenceVideoSeconds(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{
		TaskID: "task-cost", Model: "Seedance-2.5", Resolution: "720p", DurationSeconds: 30,
		TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.2,
	}
	result, cost, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{
		VideoCount: 1, BillingModel: "Seedance-2.5", VideoDurationSeconds: 30,
		VideoReferenceInputSeconds: 12,
	})
	require.NoError(t, err)
	require.Equal(t, 42, result.VideoBillingDurationSeconds)
	require.InDelta(t, 4.2, cost.TotalCost, 1e-12)
	require.InDelta(t, 8.4, cost.ActualCost, 1e-12)
	require.Equal(t, "seedance-video:task-cost", result.RequestID)
}

func TestBuildSeedanceVideoCompletionBillingRequiresReferenceVideoSeconds(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{
		TaskID: "task-missing-reference-duration", Model: "Seedance-2.5",
		Resolution: "720p", DurationSeconds: 30, ReferenceVideoCount: 1,
		TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.2,
	}

	_, _, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{
		VideoCount: 1, BillingModel: "Seedance-2.5", VideoDurationSeconds: 30,
	})
	require.ErrorContains(t, err, "reference video input duration is unavailable")
}

func TestSeedanceVideoPricingReservesMaximumReferenceVideoInput(t *testing.T) {
	t.Parallel()
	require.Equal(t, 60, seedanceVideoPricingBillingSeconds(SeedanceVideoRequestInfo{
		Model: "Seedance-2.5", DurationSeconds: 30, ReferenceVideoCount: 1,
	}))
	require.Equal(t, 30, seedanceVideoPricingBillingSeconds(SeedanceVideoRequestInfo{
		Model: "Seedance-2.5", DurationSeconds: 30,
	}))
	require.Equal(t, 15, seedanceVideoPricingBillingSeconds(SeedanceVideoRequestInfo{
		Model: "Seedance-2.0", DurationSeconds: 15, ReferenceVideoCount: 3,
	}))
}

func TestBuildSeedanceVideoCompletionBillingRejectsQueuedAndFailedResults(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{TaskID: "task-pending", DurationSeconds: 10}
	require.True(t, IsSeedanceVideoPendingStatus("queued"))
	require.True(t, IsSeedanceVideoFailedStatus("failed"))
	_, _, err := BuildSeedanceVideoCompletionBilling(pending, &OpenAIForwardResult{})
	require.Error(t, err)
}

func TestSeedanceVideoRecoveryReleasesFailedTaskHold(t *testing.T) {
	cache := &seedanceVideoCache{}
	taskRepo := &seedanceVideoTaskMemoryRepo{}
	billing := &seedanceVideoBillingRepo{}
	account := seedanceVideoTestAccount()
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"task-failed","status":"failed"}`)),
	}}}
	gateway := &OpenAIGatewayService{
		cache: cache, seedanceVideoTaskRepo: taskRepo,
		usageBillingRepo: billing, httpUpstream: upstream, cfg: &config.Config{},
	}
	pending := SeedanceVideoPendingBilling{
		AccountID: account.ID, Model: "Seedance-2.0", Resolution: "720p", DurationSeconds: 10,
		HoldID: "seedance:hold-failed", HoldAmount: 1, RequestPayloadHash: "payload-hash",
	}
	stored := storeSeedanceTestPending(t, gateway, "task-failed", 7, 8, pending)
	claimed, err := gateway.ClaimSeedanceVideoBilling(context.Background(), "task-failed", 7, 8)
	require.NoError(t, err)
	require.True(t, claimed)
	recovery := NewSeedanceVideoRecoveryService(
		gateway, nil, schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}, nil, nil,
	)

	require.NoError(t, recovery.processTask(context.Background(), stored))
	require.Len(t, billing.releases, 1)
	require.Equal(t, "seedance:hold-failed", billing.releases[0].BatchID)
	require.Equal(t, SeedanceVideoSettlementReleased, taskRepo.tasks[stored.StateID].SettlementStatus)
}

func TestSeedanceVideoRecoverySettlesCompletedTaskOnceWithFrozenPricing(t *testing.T) {
	cache := &seedanceVideoCache{}
	taskRepo := &seedanceVideoTaskMemoryRepo{}
	billing := &seedanceVideoBillingRepo{}
	account := seedanceVideoTestAccount()
	account.Status = StatusActive
	account.Schedulable = true
	user := &User{ID: 7}
	groupID := int64(9)
	apiKey := &APIKey{
		ID: 8, UserID: user.ID, User: user, GroupID: &groupID,
		Group: &Group{ID: groupID, Platform: PlatformSeedance}, Status: StatusActive,
	}
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"task-complete","status":"completed","model":"Seedance-2.5",
			"duration":30,"resolution":"720p","usage":{"reference_video_input_seconds":12}
		}`)),
	}}}
	gateway := &OpenAIGatewayService{
		cache: cache, seedanceVideoTaskRepo: taskRepo,
		usageBillingRepo: billing, httpUpstream: upstream,
		cfg: &config.Config{}, deferredService: &DeferredService{},
	}
	pending := SeedanceVideoPendingBilling{
		AccountID: account.ID, GroupID: &groupID, Model: "Seedance-2.5", Resolution: "720p",
		DurationSeconds: 30, ReferenceVideoCount: 1, HoldID: "seedance:hold-complete", HoldAmount: 6,
		RequestPayloadHash: "payload-hash", TotalCostPerSecond: 0.1,
		ActualCostPerSecond: 0.2, RateMultiplier: 2,
	}
	stored := storeSeedanceTestPending(t, gateway, "task-complete", user.ID, apiKey.ID, pending)
	claimed, err := gateway.ClaimSeedanceVideoBilling(context.Background(), "task-complete", user.ID, apiKey.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	recovery := NewSeedanceVideoRecoveryService(
		gateway, seedanceVideoAPIKeyRepo{apiKey: apiKey},
		schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}, nil, nil,
	)

	require.NoError(t, recovery.processTask(context.Background(), stored))
	require.Len(t, billing.commands, 1)
	command := billing.commands[0]
	require.Equal(t, "seedance-video:task-complete", command.RequestID)
	require.InDelta(t, 8.4, command.BalanceCost, 1e-12)
	require.Equal(t, "seedance:hold-complete", command.BalanceHoldID)
	require.InDelta(t, 6.0, command.BalanceHoldAmount, 1e-12)
	require.Equal(t, SeedanceVideoSettlementSettled, taskRepo.tasks[stored.StateID].SettlementStatus)

	// A duplicate status/content poll cannot create a second money event.
	result, err := billing.Apply(context.Background(), command)
	require.NoError(t, err)
	require.False(t, result.Applied)
}

func TestSeedanceVideoRecoveryDefersSettlementWithoutReferenceVideoSeconds(t *testing.T) {
	taskRepo := &seedanceVideoTaskMemoryRepo{}
	billing := &seedanceVideoBillingRepo{}
	account := seedanceVideoTestAccount()
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"task-reference-duration-missing","status":"completed",
			"model":"Seedance-2.5","duration":30,"resolution":"720p"
		}`)),
	}}}
	gateway := &OpenAIGatewayService{
		cache: &seedanceVideoCache{}, seedanceVideoTaskRepo: taskRepo,
		usageBillingRepo: billing, httpUpstream: upstream, cfg: &config.Config{},
	}
	stored := storeSeedanceTestPending(t, gateway, "task-reference-duration-missing", 7, 8, SeedanceVideoPendingBilling{
		AccountID: account.ID, Model: "Seedance-2.5", Resolution: "720p",
		DurationSeconds: 30, ReferenceVideoCount: 1, HoldID: "seedance:hold-missing-duration",
		HoldAmount: 12, TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.2,
	})
	recovery := NewSeedanceVideoRecoveryService(
		gateway, nil, schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}, nil, nil,
	)

	err := recovery.processTask(context.Background(), stored)
	require.ErrorContains(t, err, "reference video input duration is unavailable")
	require.Empty(t, billing.commands)
	require.Empty(t, billing.releases)
	require.Equal(t, SeedanceVideoSettlementPending, taskRepo.tasks[stored.StateID].SettlementStatus)
}
