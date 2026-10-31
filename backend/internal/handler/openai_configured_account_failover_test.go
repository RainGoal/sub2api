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
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const configuredAccount404Body = `{"error":{"code":"upstream_error","message":"Model \"deepseek-v4-pro\" is not supported by any configured account in this group"}}`

type configuredAccount404Upstream struct {
	service.HTTPUpstream
	accountIDs   []int64
	models       []string
	healthyFirst bool
	failAll      bool
	ordinary404  bool
}

func (u *configuredAccount404Upstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.accountIDs = append(u.accountIDs, accountID)
	u.models = append(u.models, gjson.GetBytes(body, "model").String())
	if (!u.healthyFirst && accountID == 9910) || u.failAll {
		response := configuredAccount404Body
		if u.ordinary404 {
			response = `{"error":{"message":"Unknown request URL"}}`
		}
		return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	}
	response := fmt.Sprintf(`{"id":"resp_healthy","object":"response","model":%q,"status":"completed","output":[{"id":"msg_healthy","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"healthy answer","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, u.models[len(u.models)-1])
	if gjson.GetBytes(body, "stream").Bool() {
		response = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"healthy answer\"}\n\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

type configuredAccount404RuleRepo struct {
	service.ErrorPassthroughRepository
	rule *model.ErrorPassthroughRule
}

func (r *configuredAccount404RuleRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return []*model.ErrorPassthroughRule{r.rule}, nil
}

func newConfiguredAccount404Handler(t *testing.T, upstream *configuredAccount404Upstream, switches int, passthrough bool) *OpenAIGatewayHandler {
	t.Helper()
	accounts := []service.Account{}
	for i, mapped := range []string{"deepseek-v4-pro", "deepseek-v4-pro-0813"} {
		accounts = append(accounts, service.Account{
			ID: int64(9910 + i), Name: fmt.Sprintf("account-%d", i), Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: i + 1,
			GroupIDs:    []int64{58},
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test", "model_mapping": map[string]any{"deepseek-v4-pro-0813": mapped}, "pool_mode": true, "pool_mode_retry_count": float64(2), "pool_mode_retry_status_codes": []any{float64(404)}},
		})
		if upstream.ordinary404 {
			accounts[i].Credentials["pool_mode"] = false
		}
		if passthrough {
			accounts[i].Extra = map[string]any{"openai_passthrough": true}
		}
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	repo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	message := "configured 404 response"
	rules := service.NewErrorPassthroughService(&configuredAccount404RuleRepo{rule: &model.ErrorPassthroughRule{ID: 2, Name: "404", Enabled: true, ErrorCodes: []int{404}, Platforms: []string{"openai"}, MatchMode: model.MatchModeAny, PassthroughCode: true, CustomMessage: &message, SkipMonitoring: true}}, nil)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, rules, nil, nil, cfg)
	h.maxAccountSwitches = switches
	return h
}

func TestConfiguredAccount404Handlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		for _, passthrough := range []bool{false, true} {
			if passthrough && endpoint != "responses" {
				continue
			}
			for _, stream := range []bool{false, true} {
				for _, scenario := range []struct {
					name         string
					healthyFirst bool
					failAll      bool
					ordinary404  bool
					switches     int
					wantCalls    []int64
					wantStatus   int
				}{
					{"normal success unchanged", true, false, false, 1, []int64{9910}, 200},
					{"recovers before passthrough", false, false, false, 1, []int64{9910, 9911}, 200},
					{"all candidates fail", false, true, false, 1, []int64{9910, 9911}, 404},
					{"switch budget exhausted", false, false, false, 0, []int64{9910}, 404},
					{"ordinary path 404 unchanged", false, false, true, 1, []int64{9910}, 404},
				} {
					t.Run(fmt.Sprintf("%s/passthrough=%t/stream=%t/%s", endpoint, passthrough, stream, scenario.name), func(t *testing.T) {
						u := &configuredAccount404Upstream{healthyFirst: scenario.healthyFirst, failAll: scenario.failAll, ordinary404: scenario.ordinary404}
						h := newConfiguredAccount404Handler(t, u, scenario.switches, passthrough)
						body := `{"model":"deepseek-v4-pro-0813","input":"hello","stream":false}`
						call := h.Responses
						if endpoint != "responses" {
							body = `{"model":"deepseek-v4-pro-0813","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`
						}
						if endpoint == "messages" {
							call = h.Messages
						}
						if endpoint == "chat/completions" {
							call = h.ChatCompletions
						}
						body = strings.Replace(body, "false", fmt.Sprint(stream), 1)
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body))
						c.Request.Header.Set("Content-Type", "application/json")
						groupID := int64(58)
						c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 795, GroupID: &groupID, User: &service.User{ID: 1703, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}})
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1703, Concurrency: 0})
						call(c)
						wantCalls := scenario.wantCalls
						if scenario.ordinary404 && endpoint == "chat/completions" {
							wantCalls = []int64{9910, 9910}
						}
						require.Equal(t, wantCalls, u.accountIDs, rec.Body.String())
						require.Equal(t, scenario.wantStatus, rec.Code, rec.Body.String())
						if scenario.wantStatus == 200 {
							require.Contains(t, rec.Body.String(), "healthy answer")
							if !passthrough {
								wantModels := []string{"deepseek-v4-pro"}
								if !scenario.healthyFirst {
									wantModels = append(wantModels, "deepseek-v4-pro-0813")
								}
								require.Equal(t, wantModels, u.models)
							}
							require.False(t, c.GetBool(service.OpsSkipPassthroughKey), "recovered attempt must not mark final error hidden")
						} else if !passthrough || !scenario.ordinary404 {
							require.True(t, c.GetBool(service.OpsSkipPassthroughKey), "final response still respects passthrough rule")
						}
					})
				}
			}
		}
	}
}
