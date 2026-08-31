package conversationaudit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractResponseNormalizesClientVisibleProtocols(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		body     string
		wantText string
	}{
		{
			name: "openai responses json", protocol: "openai_responses",
			body:     `{"output":[{"type":"reasoning","summary":[{"text":"hidden"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible"}]}]}`,
			wantText: "visible",
		},
		{
			name: "chat completions sse", protocol: "openai_chat",
			body:     "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\ndata: [DONE]\n",
			wantText: "hello",
		},
		{
			name: "anthropic sse", protocol: "anthropic_messages",
			body:     "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n",
			wantText: "answer",
		},
		{
			name: "gemini json", protocol: "gemini",
			body:     `{"candidates":[{"content":{"role":"model","parts":[{"text":"gemini answer"}]}}]}`,
			wantText: "gemini answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractResponse(tt.protocol, []byte(tt.body), 1<<20)
			require.NoError(t, err)
			require.NotEmpty(t, result.Payload.Messages)
			encoded := canonicalText(result.Payload)
			require.Contains(t, encoded, tt.wantText)
			require.NotContains(t, encoded, "hidden")
		})
	}
}

func TestExtractResponseCapturesErrorWithoutTransportFraming(t *testing.T) {
	result, err := ExtractResponse("openai_responses", []byte("data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"try later\"}}\n\n"), 1<<20)
	require.NoError(t, err)
	require.Equal(t, &CanonicalError{Code: "rate_limit_error", Message: "try later"}, result.Payload.Error)
	require.NotContains(t, canonicalText(result.Payload), "data:")
}

func TestExtractResponseCapturesFlatLocalError(t *testing.T) {
	result, err := ExtractResponse("anthropic_messages", []byte(`{"code":"SUBSCRIPTION_NOT_FOUND","message":"denied"}`), 1<<20)
	require.NoError(t, err)
	require.Equal(t, &CanonicalError{Code: "SUBSCRIPTION_NOT_FOUND", Message: "denied"}, result.Payload.Error)
}

func TestExtractResponseSegmentsHandlesSplitSSEFrames(t *testing.T) {
	result, err := ExtractResponseSegments("openai_responses", [][]byte{
		[]byte("event: response.output_text.delta\nda"),
		[]byte("ta: {\"type\":\"response.output_text.delta\",\"delta\":\"split"),
		[]byte(" frame\"}\n\n"),
	}, 1<<20)
	require.NoError(t, err)
	require.Contains(t, canonicalText(result.Payload), "split frame")
}

func TestExtractResponseOmitsVectorsAndBase64Media(t *testing.T) {
	result, err := ExtractResponse("embeddings", []byte(`{"data":[{"embedding":[0.1,0.2]},{"b64_json":"aGVsbG8="},{"url":"https://example.test/image.png"}]}`), 1<<20)
	require.NoError(t, err)
	require.Len(t, result.Payload.Messages, 1)
	items := result.Payload.Messages[0].Content
	require.Equal(t, "vector_omitted", items[0].Type)
	require.Equal(t, "media_omitted", items[1].Type)
	require.Empty(t, items[1].Content)
	require.Equal(t, "resource", items[2].Type)
}

func canonicalText(payload CanonicalConversation) string {
	var result string
	for _, message := range payload.Messages {
		for _, item := range message.Content {
			result += item.Text + item.Content + item.Arguments
		}
	}
	if payload.Error != nil {
		result += payload.Error.Code + payload.Error.Message
	}
	return result
}
