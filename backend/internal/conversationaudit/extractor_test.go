package conversationaudit

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractRequestProtocolShapes(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		body     string
		wantRole string
		wantText string
	}{
		{name: "chat", protocol: "openai_chat_completions", body: `{"messages":[{"role":"user","content":"hello"}]}`, wantRole: "user", wantText: "hello"},
		{name: "claude", protocol: "anthropic_messages", body: `{"system":"policy","messages":[{"role":"user","content":[{"type":"text","text":"claude"}]}]}`, wantRole: "system", wantText: "policy"},
		{name: "responses", protocol: "openai_responses", body: `{"instructions":"policy","input":[{"role":"user","content":[{"type":"input_text","text":"response input"}]}]}`, wantRole: "system", wantText: "policy"},
		{name: "gemini", protocol: "gemini", body: `{"contents":[{"role":"user","parts":[{"text":"gemini input"}]}]}`, wantRole: "user", wantText: "gemini input"},
		{name: "embeddings", protocol: "embeddings", body: `{"input":["one","two"]}`, wantRole: "user", wantText: "one"},
		{name: "media prompt", protocol: "openai_images", body: `{"prompt":"draw it"}`, wantRole: "user", wantText: "draw it"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ExtractRequest(test.protocol, []byte(test.body), 1<<20)
			require.NoError(t, err)
			require.NotEmpty(t, result.Payload.Messages)
			require.Equal(t, test.wantRole, result.Payload.Messages[0].Role)
			require.Equal(t, test.wantText, result.Payload.Messages[0].Content[0].Text)
		})
	}
}

func TestExtractRequestBatchImagesPreservesPromptsAndOmitsReferences(t *testing.T) {
	body := `{"request":{"items":[{"prompt":"first prompt"},{"prompt":"second prompt","reference_images":[{"data":"QUJDRA=="}]}]}}`
	result, err := ExtractRequest("openai_images", []byte(body), 1<<20)

	require.NoError(t, err)
	require.Len(t, result.Payload.Messages, 2)
	require.Equal(t, "first prompt", result.Payload.Messages[0].Content[0].Text)
	require.Equal(t, "second prompt", result.Payload.Messages[1].Content[0].Text)
	require.Equal(t, "media_omitted", result.Payload.Messages[1].Content[1].Type)
	require.NotContains(t, string(mustJSON(t, result.Payload)), "QUJDRA")
}

func TestExtractRequestPreservesToolsAndOmitsMedia(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJDRA=="}}]},{"role":"assistant","tool_calls":[{"function":{"name":"search","arguments":"{\"q\":\"term\"}"}}]}]}`
	result, err := ExtractRequest("openai_chat_completions", []byte(body), 1<<20)
	require.NoError(t, err)
	require.Len(t, result.Payload.Messages, 2)
	require.Equal(t, "media_omitted", result.Payload.Messages[0].Content[1].Type)
	require.NotContains(t, string(mustJSON(t, result.Payload)), "QUJDRA")
	require.Equal(t, "tool_call", result.Payload.Messages[1].Content[0].Type)
	require.Equal(t, "search", result.Payload.Messages[1].Content[0].Name)
}

func TestExtractRequestOmitsGeminiInlineData(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"describe"},{"inlineData":{"mimeType":"image/png","data":"QUJDRA=="}}]}]}`
	result, err := ExtractRequest("gemini", []byte(body), 1<<20)
	require.NoError(t, err)
	require.Len(t, result.Payload.Messages[0].Content, 2)
	require.Equal(t, "media_omitted", result.Payload.Messages[0].Content[1].Type)
	require.NotContains(t, string(mustJSON(t, result.Payload)), "QUJDRA")
}

func TestExtractRequestUnsupportedNeverFallsBackToRawBody(t *testing.T) {
	secret := strings.Repeat("secret", 20)
	result, err := ExtractRequest("unknown", []byte(`{"credential":"`+secret+`"}`), 1<<20)
	require.True(t, errors.Is(err, ErrUnsupportedPayload))
	require.Equal(t, "unsupported_payload", result.Reason)
	require.NotContains(t, string(mustJSON(t, result)), secret)
}

func TestExtractRequestNonJSONSTTIsMetadataOnly(t *testing.T) {
	result, err := ExtractRequest("stt", []byte("binary-media-canary"), 1<<20)
	require.NoError(t, err)
	require.Equal(t, "non_json_media_omitted", result.Reason)
	require.Equal(t, "media_omitted", result.Payload.Messages[0].Content[0].Type)
	require.NotContains(t, string(mustJSON(t, result)), "binary-media-canary")
}

func TestExtractRequestNormalizesRealtimeTextAndOmitsAudio(t *testing.T) {
	textResult, err := ExtractRequest("grok_realtime", []byte(`{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"realtime prompt"}]}}`), 1<<20)
	require.NoError(t, err)
	require.Contains(t, string(mustJSON(t, textResult.Payload)), "realtime prompt")

	audioResult, err := ExtractRequest("openai_live", []byte(`{"type":"input_audio_buffer.append","audio":"QUJDRA=="}`), 1<<20)
	require.NoError(t, err)
	require.Equal(t, "media_omitted", audioResult.Payload.Messages[0].Content[0].Type)
	require.EqualValues(t, 4, audioResult.Payload.Messages[0].Content[0].EncodedBytes)
	require.NotContains(t, string(mustJSON(t, audioResult.Payload)), "QUJDRA")
}
