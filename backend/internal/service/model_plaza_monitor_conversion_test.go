package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestModelPlazaMonitorConversionPreservesBudget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.MinOutputTokens = 4096
	for _, budget := range []int{8, 16} {
		ctx := WithModelPlazaMonitorProbe(context.Background())
		chat, err := chatCompletionsToResponses(cfg, &apicompat.ChatCompletionsRequest{
			Model: "gpt-4.1", MaxTokens: &budget,
		}, ctx)
		require.NoError(t, err)
		require.Equal(t, budget, *chat.MaxOutputTokens)
		anthropic, err := anthropicToResponses(cfg, &apicompat.AnthropicRequest{
			Model: "claude-sonnet-4-5", MaxTokens: budget,
		}, ctx)
		require.NoError(t, err)
		require.Equal(t, budget, *anthropic.MaxOutputTokens)
	}
	ordinary, err := chatCompletionsToResponses(cfg, &apicompat.ChatCompletionsRequest{
		Model: "gpt-4.1", MaxTokens: func() *int { value := 8; return &value }(),
	}, context.Background())
	require.NoError(t, err)
	require.Equal(t, 4096, *ordinary.MaxOutputTokens)
	require.Equal(t, 4096, requestMinOutputTokens(cfg, WithChannelMonitorProbe(context.Background())))
}

func TestModelPlazaMonitorChatForwardKeepsSmallUpstreamCap(t *testing.T) {
	body := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}],"max_tokens":8,"stream":false}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `data: {"type":"response.completed","response":{"id":"r1","model":"gpt-4.1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}`+"\n\n"),
	}}
	s := newOpenAIRejectedFieldTestService(upstream)
	c := newOpenAIRejectedFieldTestContext(body)
	ctx := WithModelPlazaMonitorProbe(context.Background())
	c.Request = c.Request.WithContext(ctx)
	_, err := s.ForwardAsChatCompletions(ctx, c, newOpenAIRejectedFieldTestAccount(), body, "", "")
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, int64(8), gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int())
}
