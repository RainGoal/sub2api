package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/conversationaudit"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type conversationAuditAdminServiceStub struct {
	save        func(context.Context, conversationaudit.UpdateConfigRequest, int64) (conversationaudit.PublicConfig, error)
	list        func(context.Context, conversationaudit.ListFilter, string) (conversationaudit.RecordPage, error)
	detail      func(context.Context, time.Time, uuid.UUID) (conversationaudit.RecordDetailView, error)
	deleteToken func(context.Context, string, int64) (conversationaudit.DeleteResult, error)
}

func (*conversationAuditAdminServiceStub) Config() (conversationaudit.PublicConfig, error) {
	return conversationaudit.PublicConfig{}, nil
}
func (s *conversationAuditAdminServiceStub) SaveConfig(ctx context.Context, request conversationaudit.UpdateConfigRequest, actorID int64) (conversationaudit.PublicConfig, error) {
	return s.save(ctx, request, actorID)
}
func (*conversationAuditAdminServiceStub) Runtime() conversationaudit.RuntimeState {
	return conversationaudit.RuntimeState{Lifecycle: "disabled"}
}
func (s *conversationAuditAdminServiceStub) List(ctx context.Context, filter conversationaudit.ListFilter, cursor string) (conversationaudit.RecordPage, error) {
	if s.list == nil {
		return conversationaudit.RecordPage{}, nil
	}
	return s.list(ctx, filter, cursor)
}
func (s *conversationAuditAdminServiceStub) Detail(ctx context.Context, day time.Time, auditID uuid.UUID) (conversationaudit.RecordDetailView, error) {
	if s.detail == nil {
		return conversationaudit.RecordDetailView{}, conversationaudit.ErrRecordNotFound
	}
	return s.detail(ctx, day, auditID)
}
func (*conversationAuditAdminServiceStub) DeleteOne(context.Context, time.Time, uuid.UUID) error {
	return nil
}
func (*conversationAuditAdminServiceStub) PreviewDelete(context.Context, conversationaudit.ListFilter, int64) (conversationaudit.DeletePreviewView, error) {
	return conversationaudit.DeletePreviewView{}, nil
}
func (s *conversationAuditAdminServiceStub) DeleteByToken(ctx context.Context, token string, actorID int64) (conversationaudit.DeleteResult, error) {
	return s.deleteToken(ctx, token, actorID)
}

func conversationAuditHandlerRouter(service conversationAuditAdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 42})
		c.Set(string(servermiddleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	handler := &ConversationAuditHandler{service: service}
	group := router.Group("/admin/conversation-audit")
	group.PUT("/config", handler.UpdateConfig)
	group.GET("/records", handler.ListRecords)
	group.GET("/records/:date/:id", handler.GetRecord)
	group.POST("/delete-by-filter", handler.DeleteByFilter)
	return router
}

func conversationAuditHandlerRequest(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestConversationAuditHandlerMapsConfigConflictAndActor(t *testing.T) {
	service := &conversationAuditAdminServiceStub{save: func(_ context.Context, request conversationaudit.UpdateConfigRequest, actorID int64) (conversationaudit.PublicConfig, error) {
		require.Equal(t, int64(42), actorID)
		require.Equal(t, int64(3), request.ExpectedConfigVersion)
		return conversationaudit.PublicConfig{}, conversationaudit.ErrConfigConflict
	}}
	request := map[string]any{
		"expected_config_version": 3, "enabled": false, "retention_days": 7,
		"request_max_bytes": 1048576, "response_max_bytes": 1048576,
		"memory_budget_bytes": 268435456, "worker_count": 2, "queue_capacity": 2048,
	}
	response := conversationAuditHandlerRequest(t, conversationAuditHandlerRouter(service), http.MethodPut, "/admin/conversation-audit/config", request)
	require.Equal(t, http.StatusConflict, response.Code)
	require.Contains(t, response.Body.String(), conversationaudit.ErrorCodeConfigConflict)
}

func TestConversationAuditHandlerValidatesListWindowAndDisablesDetailCaching(t *testing.T) {
	auditID := uuid.New()
	service := &conversationAuditAdminServiceStub{detail: func(_ context.Context, day time.Time, got uuid.UUID) (conversationaudit.RecordDetailView, error) {
		require.Equal(t, "2026-08-29", day.Format("2006-01-02"))
		require.Equal(t, auditID, got)
		return conversationaudit.RecordDetailView{}, nil
	}}
	router := conversationAuditHandlerRouter(service)
	invalid := conversationAuditHandlerRequest(t, router, http.MethodGet, "/admin/conversation-audit/records", nil)
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Contains(t, invalid.Body.String(), conversationaudit.ErrorCodeQueryLimitExceeded)

	detail := conversationAuditHandlerRequest(t, router, http.MethodGet, "/admin/conversation-audit/records/2026-08-29/"+auditID.String(), nil)
	require.Equal(t, http.StatusOK, detail.Code)
	require.Equal(t, "no-store", detail.Header().Get("Cache-Control"))
}

func TestConversationAuditHandlerDeleteConfirmationErrorNeverEchoesToken(t *testing.T) {
	const token = "conversation-audit-confirmation-canary"
	service := &conversationAuditAdminServiceStub{deleteToken: func(_ context.Context, got string, actorID int64) (conversationaudit.DeleteResult, error) {
		require.Equal(t, token, got)
		require.Equal(t, int64(42), actorID)
		return conversationaudit.DeleteResult{}, errors.New("internal delete detail " + token)
	}}
	response := conversationAuditHandlerRequest(t, conversationAuditHandlerRouter(service), http.MethodPost,
		"/admin/conversation-audit/delete-by-filter", map[string]any{"confirmation_token": token, "confirm": true})
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.NotContains(t, response.Body.String(), token)
}
