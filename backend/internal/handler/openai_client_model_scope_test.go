package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIClientPrivacyRuleRepo struct {
	service.ErrorPassthroughRepository
	rule *model.ErrorPassthroughRule
}

func (r *openAIClientPrivacyRuleRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return []*model.ErrorPassthroughRule{r.rule}, nil
}

func TestOpenAIClientModelNotFoundScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		path     string
		platform string
		scoped   bool
	}{
		{"/v1/responses", service.PlatformOpenAI, true},
		{"/v1/chat/completions", service.PlatformOpenAI, true},
		{"/v1/images/generations", service.PlatformOpenAI, false},
		{"/v1/embeddings", service.PlatformOpenAI, false},
		{"/v1/audio/speech", service.PlatformGrok, false},
		{"/v1/responses", service.PlatformGrok, false},
		{"/v1/chat/completions", service.PlatformKimi, false},
	} {
		t.Run(tc.path+"/"+tc.platform, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, nil)
			c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
				Matched: true, TargetPlatform: tc.platform, PublicModel: "public-A", UpstreamModel: "private-B",
			}))
			if tc.scoped {
				service.SetOpenAIClientRequestedModel(c, "private-B")
			}
			message := "The model private-C was not found on this account"
			body := []byte(`{"error":{"type":"invalid_request_error","code":"model_not_found","param":"model","message":"` + message + `"}}`)
			(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
				StatusCode: http.StatusBadRequest, ResponseBody: body,
				ResponseHeaders: http.Header{"Retry-After": []string{"23"}},
			}, false)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "23", rec.Header().Get("Retry-After"))
			require.Equal(t, message, c.GetString(service.OpsUpstreamErrorMessageKey))
			if tc.scoped {
				message = "The requested model is unavailable."
			}
			expected := `{"error":{"type":"invalid_request_error","code":"model_not_found","param":"model","message":"` + message + `"}}`
			require.JSONEq(t, expected, rec.Body.String())
		})
	}
}

func TestOpenAIClientModelFailoverRuleScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, scoped := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream=%t/scoped=%t/passthrough=%t", stream, scoped, passthrough), func(t *testing.T) {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					if scoped {
						service.SetOpenAIClientRequestedModel(c, "public-A")
					}
					customMessage := "private-B route rejected private-C"
					responseCode := http.StatusServiceUnavailable
					rule := &model.ErrorPassthroughRule{
						Enabled: true, ErrorCodes: []int{http.StatusBadGateway}, MatchMode: model.MatchModeAny,
						Platforms: []string{service.PlatformOpenAI}, ResponseCode: &responseCode,
						CustomMessage: &customMessage, PassthroughBody: passthrough, SkipMonitoring: true,
					}
					h := &OpenAIGatewayHandler{errorPassthroughService: service.NewErrorPassthroughService(&openAIClientPrivacyRuleRepo{rule: rule}, nil)}
					upstreamMessage := "private-C upstream failed"
					body := []byte(`{"error":{"message":"private-C upstream failed"}}`)
					h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
						StatusCode: http.StatusBadGateway, ResponseBody: body,
						ResponseHeaders: http.Header{"Retry-After": []string{"23"}},
					}, stream)
					if stream {
						require.Equal(t, http.StatusOK, rec.Code)
						require.Contains(t, rec.Body.String(), "data:")
					} else {
						require.Equal(t, responseCode, rec.Code)
					}
					require.Equal(t, "23", rec.Header().Get("Retry-After"))
					require.True(t, c.GetBool(service.OpsSkipPassthroughKey))
					if scoped {
						require.Contains(t, rec.Body.String(), "Upstream request failed")
						require.NotContains(t, rec.Body.String(), "private-")
						require.Equal(t, upstreamMessage, c.GetString(service.OpsUpstreamErrorMessageKey))
					} else if passthrough {
						require.Contains(t, rec.Body.String(), upstreamMessage)
					} else {
						require.Contains(t, rec.Body.String(), customMessage)
					}
				})
			}
		}
	}
}

func TestOpenAIClientModelAdmissionScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct {
		path string
		run  func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{"/v1/responses", (*OpenAIGatewayHandler).Responses},
		{"/v1/chat/completions", (*OpenAIGatewayHandler).ChatCompletions},
		{"/v1/messages", (*OpenAIGatewayHandler).Messages},
	} {
		for _, platform := range []string{service.PlatformOpenAI, service.PlatformGrok, service.PlatformKimi,
			service.PlatformZhipu, service.PlatformDeepseek, service.PlatformMiniMax} {
			for _, composite := range []bool{false, true} {
				for _, requestedModel := range []string{"public-alias", "public\x01alias"} {
					name := endpoint.path + "/" + platform + "/" + requestedModel
					if composite {
						name += "/composite"
					}
					t.Run(name, func(t *testing.T) {
						h := newServiceTierHandlerTest(t)
						t.Cleanup(h.billingCacheService.Stop)
						cache := &helperConcurrencyCacheStub{}
						h.concurrencyHelper = NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)
						body, err := json.Marshal(map[string]any{
							"model": requestedModel, "input": "hello", "stream": false, "max_tokens": 16,
							"messages": []any{map[string]any{"role": "user", "content": "hello"}},
						})
						require.NoError(t, err)
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(string(body)))
						groupID := int64(6741)
						group := &service.Group{ID: groupID, Platform: platform, AllowMessagesDispatch: true}
						if composite {
							group.Platform = service.PlatformComposite
							c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), platform))
						}
						c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
							ID: 6742, GroupID: &groupID, Group: group, User: &service.User{ID: 6743, Status: service.StatusActive},
						})
						c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 6743, Concurrency: 1})
						endpoint.run(h, c)
						require.Equal(t, platform == service.PlatformOpenAI, service.HasOpenAIClientRequestedModel(c))
						if platform == service.PlatformOpenAI && strings.ContainsRune(requestedModel, '\x01') {
							require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
							require.Contains(t, rec.Body.String(), "without control characters")
							require.Zero(t, cache.userAcquireCalls)
						} else {
							require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
							require.Equal(t, 1, cache.userAcquireCalls)
						}
					})
				}
			}
		}
	}
}
