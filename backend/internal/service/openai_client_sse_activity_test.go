package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIClientActivitySettingsProbe struct {
	SettingRepository
	timeoutLookups atomic.Int64
}

func (p *openAIClientActivitySettingsProbe) GetValue(_ context.Context, key string) (string, error) {
	if key == SettingKeyStreamTimeoutSettings {
		p.timeoutLookups.Add(1)
	}
	return "", ErrSettingNotFound
}

func TestOpenAIClientSSEContinuousMultilineActivityDoesNotTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &openAIClientActivitySettingsProbe{}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 1,
			MaxLineSize:               defaultMaxLineSize,
		}},
		toolCorrector: NewCodexToolCorrector(),
		rateLimitService: &RateLimitService{
			settingService: &SettingService{settingRepo: settings},
		},
	}
	reader, writer := io.Pipe()
	writeResult := make(chan error, 1)
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	go func() {
		defer func() { _ = writer.Close() }()
		// A complete valid event takes longer than the idle timeout, while
		// every physical line arrives well within that timeout.
		lines := []string{
			"event: response.completed", `data: {`, `data: "type":"response.completed",`,
			`data: "response":{`, `data: "id":"resp_activity",`, `data: "object":"response",`,
			`data: "model":"private-C",`, `data: "status":"completed",`, `data: "output":[],`,
			`data: "usage":{`, `data: "input_tokens":13,`, `data: "output_tokens":7`,
			`data: }`, `data: }`, `data: }`, "",
		}
		for _, line := range lines {
			if _, err := io.WriteString(writer, line+"\n"); err != nil {
				writeResult <- err
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		writeResult <- nil
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}
	account := &Account{ID: 51, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "public-A", "mapped-B")
	require.NoError(t, err, "physical upstream activity must keep the idle timer alive")
	require.NoError(t, <-writeResult)
	require.NotNil(t, result)
	require.Equal(t, 13, result.usage.InputTokens)
	require.Equal(t, 7, result.usage.OutputTokens)
	require.Zero(t, settings.timeoutLookups.Load(), "HandleStreamTimeout must not evaluate account penalties")
	require.NotContains(t, rec.Body.String(), "stream_timeout")
	require.NotContains(t, rec.Body.String(), "private-C")
	require.Contains(t, rec.Body.String(), `"model":"public-A"`)
	assertOpenAISSEFrames(t, rec.Body.String(), []string{"response.completed"})
}
