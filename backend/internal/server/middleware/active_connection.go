package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ActiveConnection tracks only authenticated AI gateway requests. Runtime
// failures are deliberately isolated from the request path by the no-op
// behavior of ActiveConnectionService and the nil-handle checks below.
func ActiveConnection(active *service.ActiveConnectionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if active == nil || !shouldTrackActiveRequest(c) {
			c.Next()
			return
		}

		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			c.Next()
			return
		}
		apiKey, _ := GetAPIKeyFromContext(c)
		requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
		requestType := activeRequestType(c.Request.URL.Path)
		apiKeyName := ""
		if apiKey != nil {
			apiKeyName = apiKey.Name
		}
		handle := active.Start(subject.UserID, service.ActiveConnectionStart{
			RequestID:   requestID,
			RequestType: requestType,
			APIKeyName:  apiKeyName,
		})
		if handle == nil {
			c.Next()
			return
		}

		c.Request = c.Request.WithContext(service.WithActiveConnection(c.Request.Context(), handle))
		defer func() {
			status := service.ActiveConnectionStatusCompleted
			if c.Writer.Status() >= http.StatusBadRequest || c.IsAborted() {
				status = service.ActiveConnectionStatusFailed
			}
			handle.Finish(status, "")
		}()
		c.Next()
	}
}

func shouldTrackActiveRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	path := strings.TrimRight(c.Request.URL.Path, "/")
	responsesPath := path == "/v1/responses" || path == "/responses" || path == "/backend-api/codex/responses"
	if c.Request.Method == http.MethodGet && responsesPath {
		return true
	}
	if c.Request.Method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/messages", "/messages", "/v1/chat/completions", "/chat/completions", "/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/embeddings", "/embeddings", "/v1/images/generations", "/images/generations", "/v1/images/edits", "/images/edits":
		return true
	default:
		return false
	}
}

func activeRequestType(path string) string {
	switch {
	case strings.Contains(path, "/chat/completions"):
		return "chat_completions"
	case strings.Contains(path, "/responses"):
		return "responses"
	case strings.Contains(path, "/messages"):
		return "messages"
	case strings.Contains(path, "/embeddings"):
		return "embeddings"
	case strings.Contains(path, "/images/"):
		return "images"
	default:
		return "gateway"
	}
}
