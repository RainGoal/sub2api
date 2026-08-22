package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ActiveConnectionHandler struct {
	active *service.ActiveConnectionService
}

func NewActiveConnectionHandler(active *service.ActiveConnectionService) *ActiveConnectionHandler {
	return &ActiveConnectionHandler{active: active}
}

// Events streams best-effort runtime state to the authenticated user panel.
// It intentionally bypasses the normal JSON envelope because SSE clients need
// raw event framing and the endpoint never participates in request billing.
func (h *ActiveConnectionHandler) Events(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 || h == nil || h.active == nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusNotImplemented)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	snapshot, events, unsubscribe := h.active.Subscribe(subject.UserID)
	defer unsubscribe()
	if err := writeActiveSSE(c, flusher, "snapshot", gin.H{"connections": snapshot}); err != nil {
		return
	}

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event := <-events:
			if err := writeActiveEvent(c, flusher, event); err != nil {
				return
			}
		case <-heartbeat.C:
			h.active.PruneExpired()
			if err := writeActiveSSE(c, flusher, "heartbeat", gin.H{}); err != nil {
				return
			}
		}
	}
}

func writeActiveEvent(c *gin.Context, flusher http.Flusher, event service.ActiveConnectionEvent) error {
	switch event.Type {
	case "connection.started", "connection.updated":
		return writeActiveSSE(c, flusher, event.Type, event.Connection)
	case "connection.completed", "connection.failed":
		return writeActiveSSE(c, flusher, event.Type, gin.H{"request_id": event.RequestID, "message": event.Message})
	default:
		return nil
	}
}

func writeActiveSSE(c *gin.Context, flusher http.Flusher, event string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
