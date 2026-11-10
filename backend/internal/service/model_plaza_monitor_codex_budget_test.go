package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelPlazaMonitorCodexDoesNotSendUnboundedRequest(t *testing.T) {
	for _, entry := range []string{"responses", "chat_completions"} {
		t.Run(entry, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","input":"hi","max_output_tokens":16}`)
			upstream := &httpUpstreamRecorder{}
			s := newOpenAIRejectedFieldTestService(upstream)
			c := newOpenAIRejectedFieldTestContext(body)
			ctx := WithModelPlazaMonitorProbe(context.Background())
			c.Request = c.Request.WithContext(ctx)
			var err error
			if entry == "responses" {
				_, err = s.Forward(ctx, c, newOpenAIOAuthNamespaceTestAccount(), body)
			} else {
				_, err = s.ForwardAsChatCompletions(ctx, c, newOpenAIOAuthNamespaceTestAccount(), body, "", "")
			}
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, c.Writer.Status())
			require.Empty(t, upstream.bodies, "a protocol that strips the cap must not reach upstream")
		})
	}
}

func TestModelPlazaMonitorCodexGuardDoesNotChangeOrdinaryRequests(t *testing.T) {
	require.NoError(t, rejectUnboundedMonitorCodex(context.Background(), nil, newOpenAIOAuthNamespaceTestAccount()))
	require.NoError(t, rejectUnboundedMonitorCodex(WithModelPlazaMonitorProbe(context.Background()), nil, newOpenAIRejectedFieldTestAccount()))
	require.NoError(t, rejectUnboundedMonitorCodex(WithChannelMonitorProbe(context.Background()), nil, newOpenAIOAuthNamespaceTestAccount()))
}

func TestModelPlazaMonitorAntigravityDoesNotAddIdentityInstructions(t *testing.T) {
	ctx := WithModelPlazaMonitorProbe(context.Background())
	s := &AntigravityGatewayService{}
	for _, entry := range []string{"messages", "gemini", "compat"} {
		t.Run(entry, func(t *testing.T) {
			c := newOpenAIRejectedFieldTestContext(nil)
			var err error
			switch entry {
			case "messages":
				_, err = s.Forward(ctx, c, nil, nil, false)
			case "gemini":
				_, err = s.ForwardGemini(ctx, c, nil, "gemini-2.5-flash", "generateContent", false, nil, false)
			case "compat":
				_, err = s.prepareAntigravityCompatCall(ctx, c, nil, antigravityCompatRequest{})
			}
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, c.Writer.Status())
		})
	}
}
