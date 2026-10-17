//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoTaskReviewAdminRequiresRoleAndRejectsClientMoney(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, role, body string
		status           int
	}{
		{"unauthenticated", "", `{}`, http.StatusUnauthorized},
		{"user", "user", `{}`, http.StatusForbidden},
		{"amount", "admin", `{"action":"confirmed_failed","reason":"verified","amount":1}`, http.StatusUnprocessableEntity},
		{"missing_reason", "admin", `{"action":"confirmed_failed","reason":""}`, http.StatusUnprocessableEntity},
		{"unsupported_completed", "admin", `{"action":"confirmed_completed","reason":"verified"}`, http.StatusUnprocessableEntity},
		{"multiple_json", "admin", `{"action":"retry_poll","reason":"verified"} {}`, http.StatusUnprocessableEntity},
		{"unavailable", "admin", `{"action":"retry_poll","reason":"verified"}`, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tc.role != "" {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
					c.Set(string(middleware.ContextKeyUserRole), tc.role)
				}
			})
			h := &OpenAIGatewayHandler{}
			router.POST("/reviews/:state_id/resolve", h.ResolveVideoTaskReview)
			request := httptest.NewRequest(http.MethodPost, "/reviews/state-1/resolve", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
		})
	}
}

func TestVideoTaskReviewResponseOmitsExecutionAndRawErrors(t *testing.T) {
	task := &service.SeedanceVideoPendingBilling{
		StateID: "vid_00112233445566778899aabbccddeeff", IDVersion: 1, TaskID: "supplier-task",
		ExecutionSnapshot: &service.VideoTasksExecutionSnapshot{BaseURL: "https://supplier-private.example/v1"},
	}
	encoded, err := json.Marshal(videoTaskReviewResponse(task))
	require.NoError(t, err)
	require.Contains(t, string(encoded), task.StateID)
	require.Contains(t, string(encoded), task.TaskID)
	require.NotContains(t, string(encoded), "supplier-private.example")
	require.NotContains(t, string(encoded), "execution_snapshot")
	require.NotContains(t, string(encoded), "last_error")
}
