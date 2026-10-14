package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestAuditRawDownloadInterruptsHTTPStream(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		name := "HTTP1"
		if http2 {
			name = "HTTP2"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			h := &RequestAuditHandler{reads: make(chan struct{}, 2), store: requestAuditStoreStub{
				read: func(_, _ string, _, limit int64, _ bool) (requestaudit.PayloadResult, error) {
					calls++
					if calls == 3 {
						return requestaudit.PayloadResult{}, errors.New("late storage failure")
					}
					return requestaudit.PayloadResult{
						Bytes:      bytes.Repeat([]byte{'x'}, int(limit)),
						TotalBytes: 2 * requestAuditReadChunk, CaptureState: "complete",
					}, nil
				},
			}}
			router := gin.New()
			router.Use(middleware.Recovery(), func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
				c.Set(string(middleware.ContextKeyUserRole), "admin")
			})
			router.GET("/audit/:id", h.Payload)
			server := httptest.NewUnstartedServer(router)
			server.EnableHTTP2 = http2
			server.StartTLS()
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/audit/"+testRequestAuditID+"?mode=raw", nil)
			require.NoError(t, err)
			resp, err := server.Client().Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			wantProto := 1
			if http2 {
				wantProto = 2
			}
			require.Equal(t, wantProto, resp.ProtoMajor)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, 2*requestAuditReadChunk, resp.ContentLength)
			body, readErr := io.ReadAll(resp.Body)
			require.Error(t, readErr, "client must not see a successful truncated download")
			require.Less(t, int64(len(body)), resp.ContentLength)
			require.NotEmpty(t, body, "test must exercise an interrupted response after payload bytes")
			require.Equal(t, bytes.Repeat([]byte{'x'}, len(body)), body, "failure must not append JSON to raw content")
			require.Eventually(t, func() bool { return len(h.reads) == 0 }, time.Second, time.Millisecond)
		})
	}
}
