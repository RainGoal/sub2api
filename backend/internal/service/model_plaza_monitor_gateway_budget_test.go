package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type modelPlazaMonitorGatewaySettings struct{ SettingRepository }

func (modelPlazaMonitorGatewaySettings) GetValue(context.Context, string) (string, error) {
	return "", ErrSettingNotFound
}

func (modelPlazaMonitorGatewaySettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

// Exercise the actual forwarding loops: the monitor's small inbound request
// must never turn into an uncapped retry when an upstream rejects its limit.
func TestModelPlazaMonitorGatewayKeepsRejectedOutputLimit(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "forward"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":16,"input":"hi"}`)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens","type":"invalid_request_error"}}`),
				newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
			}}
			svc := newOpenAIRejectedFieldTestService(upstream)
			c := newOpenAIRejectedFieldTestContext(body)
			ctx := WithModelPlazaMonitorProbe(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			account := newOpenAIRejectedFieldTestAccount()
			var err error
			if passthrough {
				_, err = svc.forwardOpenAIPassthrough(ctx, c, account, body, body, "gpt-5.5", false, nil, false, time.Now())
			} else {
				_, err = svc.Forward(ctx, c, account, body)
			}
			require.Error(t, err)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, int64(16), gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int())
		})
	}
}

func TestModelPlazaMonitorAnthropicDoesNotRectifySmallBudget(t *testing.T) {
	resetGatewayForwardingSettingsCacheForTest(t)
	body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"thinking.budget_tokens must be >= 1024"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":1,"output_tokens":1}}`),
	}}
	c := newOpenAIRejectedFieldTestContext(body)
	c.Request.URL.Path = "/v1/messages"
	ctx := WithModelPlazaMonitorProbe(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	account := newAnthropicAPIKeyAccountForTest()
	account.Extra["anthropic_passthrough"] = false
	svc := &GatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream, rateLimitService: &RateLimitService{}, deferredService: &DeferredService{},
		settingService: &SettingService{settingRepo: modelPlazaMonitorGatewaySettings{}},
	}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	_, err = svc.Forward(ctx, c, account, parsed)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, int64(8), gjson.GetBytes(upstream.bodies[0], "max_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "thinking").Exists())
}

func TestModelPlazaMonitorAnthropicOAuthDoesNotAddIdentityInstructions(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
			upstream := &httpUpstreamRecorder{}
			c := newOpenAIRejectedFieldTestContext(body)
			ctx := WithModelPlazaMonitorProbe(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			svc := &GatewayService{httpUpstream: upstream}
			account := &Account{Platform: PlatformAnthropic, Type: accountType}
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			_, err = svc.Forward(ctx, c, account, parsed)
			require.ErrorContains(t, err, "extra identity instructions")
			require.Equal(t, http.StatusBadRequest, c.Writer.Status())
			require.Empty(t, upstream.bodies)
			require.JSONEq(t, string(body), string(parsed.Body.Bytes()))
		})
	}
}
