package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsOpenAIProviderTimeoutIsNarrow(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI}
	usage := OpenAIUsage{CacheReadInputTokens: 1000, OutputTokens: 1000}

	require.True(t, isOpenAIProviderTimeout(account, usage, []byte(`{"message":"Request exceeded 100s limit: slow"}`)))
	require.False(t, isOpenAIProviderTimeout(account, OpenAIUsage{CacheReadInputTokens: 999, OutputTokens: 1000}, []byte(`Request exceeded 100s limit:`)))
	require.False(t, isOpenAIProviderTimeout(&Account{Platform: PlatformGrok}, usage, []byte(`Request exceeded 100s limit:`)))
	require.False(t, isOpenAIProviderTimeout(account, usage, []byte(`normal response`)))
	require.True(t, openAIProviderTimeoutMarkerInText("REQUEST\nEXCEEDED 100S LIMIT: details"))
	require.True(t, isOpenAIProviderTimeout(account, usage, []byte(`{"message":"Request exceeded 300s limit: slow"}`)))
	require.True(t, openAIProviderTimeoutMarkerInText("Request exceeded 300s limit: details"))
	require.False(t, openAIProviderTimeoutMarkerInText("Request exceeded 301s limit: details"))
}

func TestOpenAIProviderTimeoutNonStreamingResponseReturns502(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	body := []byte(`{"id":"resp_timeout","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Request exceeded 300s limit: Your prompt took too long to process"}]}],"usage":{"input_tokens":2000,"output_tokens":1000,"input_tokens_details":{"cached_tokens":1000}}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	svc := &OpenAIGatewayService{}
	result, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{Platform: PlatformOpenAI}, "gpt-5.4", "gpt-5.4")

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), openAIProviderTimeoutMessage)
}

func TestOpenAIProviderTimeoutSSEConvertedResponseReturns502(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	body := []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_timeout\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Request exceeded 100s limit: slow\"}]}],\"usage\":{\"input_tokens\":2000,\"output_tokens\":1000,\"input_tokens_details\":{\"cached_tokens\":1000}}}}\n\ndata: [DONE]\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	result, err := (&OpenAIGatewayService{}).handleSSEToJSON(resp, c, &Account{Platform: PlatformOpenAI}, body, "gpt-5.4", "gpt-5.4")

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, rec.Code)
}
