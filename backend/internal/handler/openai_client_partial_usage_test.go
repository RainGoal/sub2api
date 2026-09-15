package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIClientPartialUsageUpstream struct {
	service.HTTPUpstream
	body       string
	transient  bool
	accountIDs []int64
}

func (u *openAIClientPartialUsageUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, accountID)
	status, contentType, body := http.StatusOK, "text/event-stream", u.body
	if u.transient {
		status, contentType, body = http.StatusBadGateway, "application/json", `{"error":{"message":"temporary upstream failure"}}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {contentType}, "X-Request-Id": {"rid_partial_image"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestOpenAIClientPartialUsageResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, malformed := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/malformed=%t", passthrough, malformed), func(t *testing.T) {
				runOpenAIClientPartialUsageResponses(t, passthrough, malformed)
			})
		}
	}
}

func runOpenAIClientPartialUsageResponses(t *testing.T, passthrough, malformed bool) {
	t.Helper()
	const request = `{"model":"gpt-5.2","input":"Draw the image.","tools":[{"type":"image_generation","model":"gpt-image-1","size":"1024x1024"}],"stream":true}`
	const imageItem = `{"id":"ig_partial","type":"image_generation_call","status":"completed","result":"aW1hZ2U=","size":"1024x1024","revised_prompt":"Draw text mentioning private-C exactly"}`
	const usage = `{"input_tokens":11,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}`
	terminal := `{"type":"response.completed","response":{"id":"resp_partial","model":"private-C","status":"completed","output":[` + imageItem + `],"usage":` + usage + `}}`
	if malformed {
		terminal = `{"type":"response.completed","response":{"model":"private-C"}`
	}
	upstream := &openAIClientPartialUsageUpstream{body: strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_partial","model":"private-C"}}`, "",
		`data: {"type":"response.output_text.delta","delta":"The image label says private-C."}`, "",
		`data: {"type":"response.output_item.done","item":` + imageItem + `}`, "",
		`data: {"type":"response.usage","usage":` + usage + `}`, "",
		"data: " + terminal, "", "",
	}, "\n")}
	account := service.Account{
		ID: 9920, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Priority: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
		Extra:       map[string]any{"openai_passthrough": passthrough},
	}
	fallback := account
	fallback.ID, fallback.Priority = 9921, 2
	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account, fallback}}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Security.URLAllowlist.Enabled = false
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		accountRepo, usageRepo, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), service.NewRateLimitService(accountRepo, nil, cfg, nil, nil),
		billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	groupID := int64(4220)
	apiKey := &service.APIKey{
		ID: 1820, GroupID: &groupID, User: &service.User{ID: 1720, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive,
			AllowImageGeneration: true, RateMultiplier: 1},
	}
	newContext := func() (*gin.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(request))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 0})
		return c, rec
	}
	transientFailure := func() {
		upstream.transient = true
		c, _ := newContext()
		result, err := gateway.Forward(context.Background(), c, &account, []byte(request))
		var failover *service.UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Nil(t, result)
		upstream.transient = false
	}
	excludeFallback := map[int64]struct{}{fallback.ID: {}}
	selectPrimary := func() (*service.Account, error) {
		return gateway.SelectAccountForModelWithExclusions(context.Background(), &groupID, "", "gpt-5.2", excludeFallback)
	}
	// One transient failure leaves the account selectable. A genuine successful
	// turn clears this streak; a local presentation failure must leave it alone.
	transientFailure()
	selected, err := selectPrimary()
	require.NoError(t, err)
	require.Equal(t, account.ID, selected.ID)
	callsBefore := len(upstream.accountIDs)
	c, rec := newContext()
	h.Responses(c)
	require.Equal(t, []int64{account.ID}, upstream.accountIDs[callsBefore:], "partial output must never replay on another account")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "The image label says private-C.")
	require.Contains(t, rec.Body.String(), imageItem)
	terminalType := "response.completed"
	if malformed {
		terminalType = "response.failed"
		require.Contains(t, rec.Body.String(), "Upstream request failed")
	}
	terminalCount := 0
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		require.True(t, gjson.Valid(payload), "client receives only valid JSON frames")
		if model := gjson.Get(payload, "response.model"); model.Exists() {
			require.Equal(t, "gpt-5.2", model.String())
		}
		if gjson.Get(payload, "type").String() == terminalType {
			terminalCount++
		}
	}
	require.Equal(t, 1, terminalCount)
	require.Len(t, usageRepo.created, 1, "already observed image usage must be persisted exactly once")
	log := <-usageRepo.created
	require.Equal(t, account.ID, log.AccountID)
	require.Equal(t, 1, log.ImageCount, "terminal output must not duplicate the earlier completed image")
	require.Equal(t, 9, log.InputTokens)
	require.Equal(t, 2, log.CacheReadTokens)
	require.Equal(t, 3, log.OutputTokens)
	require.NotNil(t, log.UpstreamResponseModel)
	require.Equal(t, "private-C", *log.UpstreamResponseModel)

	transientFailure()
	selected, err = selectPrimary()
	if malformed {
		require.ErrorIs(t, err, service.ErrNoAvailableAccounts, "local payload failure must not clear the existing transient streak as a scheduling success")
		require.Nil(t, selected)
	} else {
		require.NoError(t, err, "normal completion must still clear the old transient streak")
		require.Equal(t, account.ID, selected.ID)
	}
}
