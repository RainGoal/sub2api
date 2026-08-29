package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/conversationaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type conversationAuditRecorderStub struct {
	enabled    bool
	panicBegin bool
	begin      []conversationaudit.BeginInput
	session    *conversationAuditSessionStub
}

type conversationAuditAPIKeyRepository struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r conversationAuditAPIKeyRepository) GetByKey(_ context.Context, key string) (*service.APIKey, error) {
	return r.lookup(key)
}

func (r conversationAuditAPIKeyRepository) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	return r.lookup(key)
}

func (r conversationAuditAPIKeyRepository) lookup(key string) (*service.APIKey, error) {
	if r.key == nil || key != r.key.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.key
	return &clone, nil
}

func (r *conversationAuditRecorderStub) EffectiveEnabled() bool { return r != nil && r.enabled }

func (r *conversationAuditRecorderStub) Begin(_ context.Context, input conversationaudit.BeginInput) conversationaudit.Session {
	if r.panicBegin {
		panic("begin failed")
	}
	r.begin = append(r.begin, input)
	if r.session == nil {
		r.session = &conversationAuditSessionStub{}
	}
	return r.session
}

type conversationAuditSessionStub struct {
	patches       []conversationaudit.MetadataPatch
	requestBody   []byte
	requestProto  string
	responseBody  []byte
	responseProto string
	events        []conversationaudit.ResponseEvent
	finish        []conversationaudit.FinishResult
	panicObserve  bool
	panicFinish   bool
}

func (s *conversationAuditSessionStub) Annotate(patch conversationaudit.MetadataPatch) {
	s.patches = append(s.patches, patch)
}
func (s *conversationAuditSessionStub) SetRequestBody(protocol string, body []byte) {
	s.requestProto = protocol
	s.requestBody = append([]byte(nil), body...)
}
func (s *conversationAuditSessionStub) SetRequest(conversationaudit.CanonicalConversation) {}
func (s *conversationAuditSessionStub) Observe(event conversationaudit.ResponseEvent) {
	s.events = append(s.events, event)
}
func (s *conversationAuditSessionStub) ObserveResponseBytes(protocol string, body []byte) {
	if s.panicObserve {
		panic("observe failed")
	}
	s.responseProto = protocol
	s.responseBody = append(s.responseBody, body...)
}
func (s *conversationAuditSessionStub) Finish(result conversationaudit.FinishResult) {
	if s.panicFinish {
		panic("finish failed")
	}
	s.finish = append(s.finish, result)
}

func TestConversationAuditDisabledPathDoesNotAllocate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	recorder := &conversationAuditRecorderStub{enabled: false}
	key := conversationAuditTestAPIKey()

	unexpectedFinish := false
	allocations := testing.AllocsPerRun(1000, func() {
		unexpectedFinish = unexpectedFinish || beginConversationAudit(c, recorder, key) != nil
	})
	require.Zero(t, allocations)
	require.False(t, unexpectedFinish)
	require.Empty(t, recorder.begin)
}

func TestConversationAuditWriterPreservesClientResponse(t *testing.T) {
	recorder := &conversationAuditRecorderStub{enabled: true}
	c, response := newConversationAuditTestContext(http.MethodPost, "/v1/responses")
	finish := beginConversationAudit(c, recorder, conversationAuditTestAPIKey())
	require.NotNil(t, finish)

	request := []byte(`{"model":"gpt-test","input":"hello"}`)
	CaptureConversationAuditRequest(c, "openai_responses", "gpt-test", request)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
	require.NoError(t, err)
	finish()

	require.Equal(t, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", response.Body.String())
	require.Equal(t, request, recorder.session.requestBody)
	require.Equal(t, response.Body.Bytes(), recorder.session.responseBody)
	require.Len(t, recorder.session.finish, 1)
	require.Equal(t, conversationaudit.OutcomeCompleted, recorder.session.finish[0].OutcomeStatus)
	require.Equal(t, http.StatusOK, recorder.session.finish[0].HTTPStatus)
	require.Equal(t, conversationaudit.TransportSSE, recorder.session.patches[len(recorder.session.patches)-1].TransportMode)
}

func TestConversationAuditCapturesLocalErrorAndCancellation(t *testing.T) {
	t.Run("local error", func(t *testing.T) {
		recorder := &conversationAuditRecorderStub{enabled: true}
		c, response := newConversationAuditTestContext(http.MethodPost, "/v1/messages")
		finish := beginConversationAudit(c, recorder, conversationAuditTestAPIKey())
		AbortWithError(c, http.StatusForbidden, "SUBSCRIPTION_NOT_FOUND", "denied")
		finish()
		require.Equal(t, http.StatusForbidden, response.Code)
		require.Equal(t, conversationaudit.OutcomeError, recorder.session.finish[0].OutcomeStatus)
		require.Equal(t, "SUBSCRIPTION_NOT_FOUND", recorder.session.finish[0].ErrorCode)
	})

	t.Run("cancelled", func(t *testing.T) {
		recorder := &conversationAuditRecorderStub{enabled: true}
		c, _ := newConversationAuditTestContext(http.MethodPost, "/v1/responses")
		ctx, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		finish := beginConversationAudit(c, recorder, conversationAuditTestAPIKey())
		cancel()
		finish()
		require.Equal(t, conversationaudit.OutcomeCancelled, recorder.session.finish[0].OutcomeStatus)
	})
}

func TestConversationAuditClassifiesPartialStreamAndPanic(t *testing.T) {
	t.Run("partial stream", func(t *testing.T) {
		recorder := &conversationAuditRecorderStub{enabled: true}
		c, _ := newConversationAuditTestContext(http.MethodPost, "/v1/responses")
		finish := beginConversationAudit(c, recorder, conversationAuditTestAPIKey())
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
		require.NoError(t, err)
		service.MarkOpsStreamError(c, "upstream_error", "failed", http.StatusBadGateway)
		finish()
		require.Equal(t, conversationaudit.OutcomePartial, recorder.session.finish[0].OutcomeStatus)
		require.Equal(t, "upstream_error", recorder.session.finish[0].ErrorCode)
	})

	t.Run("panic is rethrown", func(t *testing.T) {
		recorder := &conversationAuditRecorderStub{enabled: true}
		c, _ := newConversationAuditTestContext(http.MethodPost, "/v1/responses")
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			finish := beginConversationAudit(c, recorder, conversationAuditTestAPIKey())
			defer finish()
			panic("boom")
		}()
		require.Equal(t, "boom", recovered)
		require.Equal(t, conversationaudit.OutcomeError, recorder.session.finish[0].OutcomeStatus)
		require.Equal(t, "panic", recorder.session.finish[0].ErrorCode)
		require.Equal(t, http.StatusInternalServerError, recorder.session.finish[0].HTTPStatus)
	})
}

func TestConversationAuditRouteManifest(t *testing.T) {
	tests := []struct {
		method   string
		path     string
		captured bool
	}{
		{http.MethodPost, "/v1/messages", true},
		{http.MethodPost, "/v1/chat/completions", true},
		{http.MethodPost, "/v1/responses", true},
		{http.MethodPost, "/backend-api/codex/responses/compact", true},
		{http.MethodPost, "/v1/embeddings", true},
		{http.MethodPost, "/v1beta/models/gemini-2.5:generateContent", true},
		{http.MethodPost, "/v1beta/models/gemini-2.5:streamGenerateContent", true},
		{http.MethodPost, "/v1/images/generations", true},
		{http.MethodPost, "/v1/videos", true},
		{http.MethodPost, "/v1/alpha/search", true},
		{http.MethodGet, "/v1/models", false},
		{http.MethodGet, "/v1/usage", false},
		{http.MethodGet, "/v1/sub2api/billing", false},
		{http.MethodPost, "/v1/messages/count_tokens", false},
		{http.MethodPost, "/v1/responses/input_tokens", false},
		{http.MethodGet, "/v1/images/tasks/task-id", false},
		{http.MethodGet, "/v1/videos/job-id/content", false},
		{http.MethodGet, "/v1/realtime", false},
	}
	for _, tt := range tests {
		_, captured := classifyConversationAuditRoute(tt.method, tt.path)
		require.Equalf(t, tt.captured, captured, "%s %s", tt.method, tt.path)
	}
}

func TestConversationAuditPanicsNeverChangeGatewayResponse(t *testing.T) {
	t.Run("begin panic disables capture", func(t *testing.T) {
		recorder := &conversationAuditRecorderStub{enabled: true, panicBegin: true}
		c, response := newConversationAuditTestContext(http.MethodPost, "/v1/responses")
		require.Nil(t, beginConversationAudit(c, recorder, conversationAuditTestAPIKey()))
		c.JSON(http.StatusOK, gin.H{"ok": true})
		require.JSONEq(t, `{"ok":true}`, response.Body.String())
	})

	t.Run("observe and finish panics are isolated", func(t *testing.T) {
		session := &conversationAuditSessionStub{panicObserve: true, panicFinish: true}
		recorder := &conversationAuditRecorderStub{enabled: true, session: session}
		c, response := newConversationAuditTestContext(http.MethodPost, "/v1/responses")
		finish := beginConversationAudit(c, recorder, conversationAuditTestAPIKey())
		require.NotNil(t, finish)
		c.JSON(http.StatusOK, gin.H{"ok": true})
		finish()
		require.JSONEq(t, `{"ok":true}`, response.Body.String())
		require.Equal(t, http.StatusOK, response.Code)
	})
}

func TestAPIKeyAuthBeginsAuditOnlyAfterCredentialResolution(t *testing.T) {
	groupID := int64(101)
	apiKey := &service.APIKey{
		ID: 100, UserID: 7, Key: "known-key", Status: service.StatusActive, GroupID: &groupID,
		User: &service.User{ID: 7, Status: service.StatusActive, Balance: 10},
		Group: &service.Group{
			ID: groupID, Name: "disabled", Status: service.StatusDisabled,
			Platform: service.PlatformAnthropic, Hydrated: true,
		},
	}
	repository := conversationAuditAPIKeyRepository{key: apiKey}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKeyService := service.NewAPIKeyService(repository, nil, nil, nil, nil, nil, cfg)
	recorder := &conversationAuditRecorderStub{enabled: true}
	router := gin.New()
	router.Use(apiKeyAuthWithSubscription(apiKeyService, nil, cfg, recorder))
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	unknown := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	unknown.Header.Set("x-api-key", "unknown-key")
	unknownResponse := httptest.NewRecorder()
	router.ServeHTTP(unknownResponse, unknown)
	require.Equal(t, http.StatusUnauthorized, unknownResponse.Code)
	require.Empty(t, recorder.begin)

	known := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	known.Header.Set("x-api-key", apiKey.Key)
	knownResponse := httptest.NewRecorder()
	router.ServeHTTP(knownResponse, known)
	require.Equal(t, http.StatusForbidden, knownResponse.Code)
	require.Len(t, recorder.begin, 1)
	require.Len(t, recorder.session.finish, 1)
	require.Equal(t, conversationaudit.OutcomeError, recorder.session.finish[0].OutcomeStatus)
	require.Equal(t, "GROUP_DISABLED", recorder.session.finish[0].ErrorCode)
}

func newConversationAuditTestContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, response
}

func conversationAuditTestAPIKey() *service.APIKey {
	groupID := int64(3)
	return &service.APIKey{
		ID: 2, UserID: 1, Name: "test-key", GroupID: &groupID,
		User:  &service.User{ID: 1, Username: "tester"},
		Group: &service.Group{ID: groupID, Name: "test-group"},
	}
}
