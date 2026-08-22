package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestActiveConnectionEventsWritesSnapshotAndStopsWhenRequestIsCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	active := service.NewActiveConnectionService()
	if active.Start(7, service.ActiveConnectionStart{RequestID: "req-7", Model: "gpt-5"}) == nil {
		t.Fatal("expected active connection")
	}
	h := NewActiveConnectionHandler(active)

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/active/events", nil).WithContext(requestContext)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})

	h.Events(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("unexpected content type: %q", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: snapshot\n") || !strings.Contains(body, `"request_id":"req-7"`) {
		t.Fatalf("snapshot did not contain the active connection: %s", body)
	}
}
