package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamingFirstTokenStartsAtFirstSSEData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const textDelay = 300 * time.Millisecond

	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			reader, writer := io.Pipe()
			writerDone := make(chan struct{})
			go func() {
				defer close(writerDone)
				defer func() { _ = writer.Close() }()
				_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_first_data\"}}\n\n")
				time.Sleep(textDelay)
				_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ready\"}\n\n")
				_, _ = io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_first_data\"}}\n\n")
			}()

			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
			started := time.Now()

			var firstTokenMs *int
			if passthrough {
				result, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, started, "model", "model")
				require.NoError(t, err)
				require.NotNil(t, result)
				firstTokenMs = result.firstTokenMs
			} else {
				result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, started, "model", "model")
				require.NoError(t, err)
				require.NotNil(t, result)
				firstTokenMs = result.firstTokenMs
			}
			require.NotNil(t, firstTokenMs)
			require.Less(t, *firstTokenMs, int(textDelay.Milliseconds()/2))
			select {
			case <-writerDone:
			case <-time.After(time.Second):
				t.Fatal("upstream writer did not exit")
			}
		})
	}
}
