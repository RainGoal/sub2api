package service

import (
	"bufio"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIClientModel_ProtocolFieldsOnly(t *testing.T) {
	for _, payload := range []string{
		`{"model":"private-C","usage":{"input_tokens":7}}`,
		`{"type":"response.completed","model":"private-C","response":{"model":"private-C","usage":{"input_tokens":7}}}`,
		`{"type":"message_start","message":{"model":"private-C"}}`,
		`{"type":"session.updated","session":{"model":"private-C"}}`,
		`{"model":null,"response":{"model":["private-C"]},"message":{"model":{"name":"private-C"}}}`,
		`{"model":"private-C","model":"other-C","usage":{"input_tokens":9007199254740993}}`,
		`{"mo\u0064el":"private-C","response":{"mo\u0064el":"private-C"},"usage":{"input_tokens":9007199254740993}}`,
		`{"response":{"model":"private-C"},"response":{"model":"other-C"}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			original := []byte(payload)
			updated, err := rewriteOpenAIClientModel(original, "public-A")
			require.NoError(t, err)
			require.JSONEq(t, payload, string(original), "source must not be mutated")
			require.NotContains(t, string(updated), "private-C")
			require.NotContains(t, string(updated), "other-C")
			for _, path := range []string{"model", "response.model", "message.model", "session.model"} {
				if value := gjson.GetBytes(updated, path); value.Exists() {
					require.Equal(t, "public-A", value.String())
				}
			}
			if strings.Contains(payload, "9007199254740993") {
				require.Equal(t, "9007199254740993", gjson.GetBytes(updated, "usage.input_tokens").Raw)
			}
		})
	}
	const body = `{"model":"private-C","output":[{"content":[{"text":"private-C"}],"arguments":"{\"model\":\"private-C\"}"}],"metadata":{"model":"private-C"},"usage":{"input_tokens":9007199254740993}}`
	updated, err := rewriteOpenAIClientModel([]byte(body), "public-A")
	require.NoError(t, err)
	require.Equal(t, strings.Replace(body, `"model":"private-C"`, `"model":"public-A"`, 1), string(updated))
}

func TestOpenAIClientModel_OptionalFieldsAndInvalidPayloads(t *testing.T) {
	for _, payload := range []string{"", "[DONE]", `{"type":"response.output_text.delta","delta":"private-C"}`, `{"type":"future.event","opaque":{"model":"private-C"}}`} {
		updated, err := rewriteOpenAIClientPayload([]byte(payload), "")
		require.NoError(t, err)
		require.Equal(t, payload, string(updated))
	}
	for _, payload := range []string{`{"model":"private-C"`, `{"model":"private-C"} trailing`, `{"model":"private-C"}`} {
		updated, err := rewriteOpenAIClientModel([]byte(payload), "")
		require.ErrorIs(t, err, errOpenAIClientPayload)
		require.Nil(t, updated)
	}
}

func TestOpenAIClientModel_PublicSnapshotSurvivesMappingAndRetries(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, PublicModel: "public-A", UpstreamModel: "channel-B", TargetPlatform: PlatformOpenAI,
	})
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	require.Equal(t, "public-A", SetOpenAIClientRequestedModel(c, "channel-B"))
	c.Request = c.Request.WithContext(context.Background())
	require.Equal(t, "public-A", SetOpenAIClientRequestedModel(c, "retry-B"))
	require.Equal(t, "public-A", openAIClientRequestedModel(c, "account-B"))
	for _, model := range []string{"", "  ", "bad\nmodel", string([]byte{0xff})} {
		require.Error(t, ValidateOpenAIClientModel(model))
	}
	for _, model := range []string{"openai/alias", `quoted"alias`, "公开模型"} {
		require.NoError(t, ValidateOpenAIClientModel(model))
	}
}

func TestOpenAIClientSSE_CompleteEventsAndOpaqueContent(t *testing.T) {
	body := ": ping\r\n\r\nid: evt_1\r\nevent: response.created\r\n" +
		"data: {\"type\":\"response.created\",\r\ndata: \"response\":{\"model\":\"private-C\",\"id\":\"resp_1\"}}\r\n\r\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"private-C\"}\n\n" +
		"data: [DONE]\n\n"
	updated, err := rewriteOpenAIClientSSEBody(body, "public-A", 4096)
	require.NoError(t, err)
	require.Contains(t, string(updated), ": ping\n\n")
	require.Contains(t, string(updated), "id: evt_1\nevent: response.created\n")
	require.Contains(t, string(updated), `"model":"public-A"`)
	require.Contains(t, string(updated), `"delta":"private-C"`)
	require.True(t, strings.HasSuffix(string(updated), "data: [DONE]\n\n"))
}

func TestOpenAIClientSSE_EventLimitAndMalformedJSON(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("event: response.created\ndata: {\ndata: \"model\":\"private-C\"}\n\n"))
	events := newOpenAIClientSSEScanner(scanner, 32)
	require.False(t, events.Scan())
	require.ErrorIs(t, events.Err(), errOpenAIClientPayload)
	updated, err := rewriteOpenAIClientSSEBody("data: {\"model\":\"private-C\"\n\n", "public-A", 4096)
	require.ErrorIs(t, err, errOpenAIClientPayload)
	require.Nil(t, updated)
	updated, err = rewriteOpenAIClientSSEBody("data: {\"model\":\"private-C\"}", "public-A", 4096)
	require.NoError(t, err)
	require.Contains(t, string(updated), `"model":"public-A"`)
}

func TestOpenAIClientSSE_IndentedJSONResponse(t *testing.T) {
	const body = "{\n\n  \"model\": \"private-C\",\n  \"output\": [{\"text\": \"private-C\"}]\n}\n"
	updated, err := rewriteOpenAIClientSSEBody(body, "public-A", 4096)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"public-A","output":[{"text":"private-C"}]}`, string(updated))
	_, err = rewriteOpenAIClientSSEBody(body, "public-A", 32)
	require.ErrorIs(t, err, errOpenAIClientPayload)
}

func TestOpenAIClientSSE_ResponseFailedPreservesRecoveryMetadata(t *testing.T) {
	const fields = `"code":"usage_limit_reached","message":"private-C rate limit reached. Please try again in 467ms.","param":"input","retry_after_ms":467,"resets_at":9007199254740993,"resets_in_seconds":"60","request_id":"opaque_id","debug":"private-C"`
	for _, source := range []string{
		`{"type":"error",` + fields + `}`,
		`{"type":"error","error":{"type":"rate_limit_error",` + fields + `}}`,
		`{"type":"response.failed","response":{"error":{"type":"rate_limit_error",` + fields + `}}}`,
	} {
		t.Run(source, func(t *testing.T) {
			body := buildOpenAIClientResponseFailedSSE("resp_original", "public-A", []byte(source), "failure")
			eventType, payload, ok := extractOpenAISSETerminalEvent(body)
			require.True(t, ok)
			require.Equal(t, "response.failed", eventType)
			require.Equal(t, "resp_original", gjson.GetBytes(payload, "response.id").String())
			require.Equal(t, "public-A", gjson.GetBytes(payload, "response.model").String())
			require.Equal(t, "usage_limit_reached", gjson.GetBytes(payload, "response.error.code").String())
			require.Equal(t, "input", gjson.GetBytes(payload, "response.error.param").String())
			require.Equal(t, "467", gjson.GetBytes(payload, "response.error.retry_after_ms").Raw)
			require.Equal(t, "9007199254740993", gjson.GetBytes(payload, "response.error.resets_at").Raw)
			require.Equal(t, "60", gjson.GetBytes(payload, "response.error.resets_in_seconds").String())
			require.Equal(t, "opaque_id", gjson.GetBytes(payload, "response.error.request_id").String())
			require.Contains(t, gjson.GetBytes(payload, "response.error.message").String(), "467ms")
			require.NotContains(t, body, "private-C")
		})
	}
}

func TestOpenAIClientSSE_ResponseFailedPreservesCapacityRetryCode(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down"} {
		source := []byte(`{"type":"error","error":{"code":"` + code + `","message":"The selected model private-C is at capacity."}}`)
		body := buildOpenAIClientResponseFailedSSE("resp_capacity", "public-A", source, "failure")
		_, payload, ok := extractOpenAISSETerminalEvent(body)
		require.True(t, ok)
		require.Equal(t, "server_error", gjson.GetBytes(payload, "response.error.code").String())
		require.NotContains(t, body, "private-C")
	}
}

func TestOpenAIClientSSE_PreservesEventHeaderOwnership(t *testing.T) {
	const created = `{"type":"response.created","response":{"model":"private-C"}}`
	const completed = `{"type":"response.completed","response":{"model":"private-C"}}`
	body := "event: response.created\ndata: " + created + "\nid: evt_2\nevent: response.completed\ndata: " + completed + "\n\n"
	updated, err := rewriteOpenAIClientSSEBody(body, "public-A", 4096)
	require.NoError(t, err)
	require.Equal(t, "event: response.created\ndata: "+strings.ReplaceAll(created, "private-C", "public-A")+
		"\nid: evt_2\nevent: response.completed\ndata: "+strings.ReplaceAll(completed, "private-C", "public-A")+"\n\n", string(updated))
	// SSE also permits the event field after data when the separator is present.
	body = "data: " + completed + "\nevent: response.completed\n\n"
	updated, err = rewriteOpenAIClientSSEBody(body, "public-A", 4096)
	require.NoError(t, err)
	require.Equal(t, strings.ReplaceAll(body, "private-C", "public-A"), string(updated))
}
