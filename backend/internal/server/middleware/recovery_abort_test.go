//go:build unit

package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRecoveryAbortHandlerPropagatesWithoutLoggingCredentialHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logs
	t.Cleanup(func() { gin.DefaultErrorWriter = previous })

	router := gin.New()
	router.Use(Recovery())
	router.GET("/download", func(c *gin.Context) {
		c.Writer.Header().Set("Content-Length", "10")
		_, _ = c.Writer.Write([]byte("first"))
		panic(http.ErrAbortHandler)
	})

	request := httptest.NewRequest(http.MethodGet, "/download", nil)
	request.Header.Set("X-Private-Key", "must-not-be-logged")
	recorder := httptest.NewRecorder()
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		router.ServeHTTP(recorder, request)
	})
	require.Equal(t, "first", recorder.Body.String())
	require.Empty(t, logs.String())
}

func TestRecoveryLogsStreamingPanicWithoutChangingResponseOrLoggingHeaders(t *testing.T) {
	var logs bytes.Buffer
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logs
	t.Cleanup(func() { gin.DefaultErrorWriter = previous })
	router := gin.New()
	router.Use(Recovery())
	router.GET("/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("data: first\n\n")
		c.Writer.Flush()
		panic("unexpected stream failure")
	})
	request := httptest.NewRequest(http.MethodGet, "/stream", nil)
	request.Header.Set("X-Private-Key", "must-not-be-logged")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "data: first\n\n", recorder.Body.String())
	require.Contains(t, logs.String(), "unexpected stream failure")
	require.Contains(t, logs.String(), "recovery_abort_test.go")
	require.NotContains(t, logs.String(), "must-not-be-logged")
}
