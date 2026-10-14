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

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const testRequestAuditID = "693b5cb0-cdef-4acf-a2e6-3f760debb1ee"

type requestAuditStoreStub struct {
	RequestAuditAdminStore
	update func(requestaudit.ConfigUpdate) (requestaudit.Config, error)
	read   func(string, string, int64, int64, bool) (requestaudit.PayloadResult, error)
	create func(requestaudit.CreateBanInput) (*requestaudit.SessionBan, error)
}

func (s requestAuditStoreStub) UpdateConfig(_ context.Context, input requestaudit.ConfigUpdate) (requestaudit.Config, error) {
	return s.update(input)
}
func (s requestAuditStoreStub) ReadPayload(_ context.Context, id, part string, offset, limit int64, partial bool) (requestaudit.PayloadResult, error) {
	return s.read(id, part, offset, limit, partial)
}
func (s requestAuditStoreStub) CreateBan(_ context.Context, input requestaudit.CreateBanInput) (*requestaudit.SessionBan, error) {
	return s.create(input)
}

func requestAuditTestRouter(role string, handle gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if role != "" {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
	})
	router.Any("/audit/:id", handle)
	return router
}

func requestAuditTestRequest(router *gin.Engine, method, query, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/audit/"+testRequestAuditID+query, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

func TestRequestAuditEndpointsRequireAdministrator(t *testing.T) {
	h := &RequestAuditHandler{store: requestAuditStoreStub{}, reads: make(chan struct{}, 2)}
	for name, handle := range map[string]gin.HandlerFunc{
		"list": h.List, "get": h.Get, "config": h.GetConfig, "update": h.UpdateConfig,
		"status": h.Status, "payload": h.Payload, "bans": h.ListBans,
		"ban": h.GetBan, "create": h.CreateBan, "revoke": h.RevokeBan,
	} {
		t.Run(name, func(t *testing.T) {
			for role, status := range map[string]int{"": 401, "user": 403} {
				w := requestAuditTestRequest(requestAuditTestRouter(role, handle), "GET", "", "")
				require.Equal(t, status, w.Code)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestRequestAuditConfigValidationAndActor(t *testing.T) {
	var updates []requestaudit.ConfigUpdate
	h := &RequestAuditHandler{store: requestAuditStoreStub{update: func(input requestaudit.ConfigUpdate) (requestaudit.Config, error) {
		updates = append(updates, input)
		return requestaudit.Config{RetentionDays: input.RetentionDays, Version: 2}, nil
	}}}
	router := requestAuditTestRouter("admin", h.UpdateConfig)
	for _, body := range []string{
		`{}`, `{"enabled":false,"session_ban_enabled":false,"retention_days":0,"expected_version":1}`,
		`{"enabled":true,"session_ban_enabled":false,"retention_days":31,"expected_version":1}`,
		`{"enabled":true,"session_ban_enabled":false,"retention_days":3}`,
	} {
		require.Equal(t, 400, requestAuditTestRequest(router, "PUT", "", body).Code)
	}
	require.Empty(t, updates)
	w := requestAuditTestRequest(router, "PUT", "", `{"enabled":false,"session_ban_enabled":false,"retention_days":3,"expected_version":1,"updated_by":999}`)
	require.Equal(t, 200, w.Code)
	require.Len(t, updates, 1)
	require.Equal(t, int64(77), updates[0].UpdatedBy)
	require.False(t, updates[0].Enabled)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":false,"retention_days":3,"session_ban_enabled":false,"version":2,"updated_at":"0001-01-01T00:00:00Z","updated_by":0}}`, w.Body.String())
}

func TestRequestAuditBanActorAndUncertainResult(t *testing.T) {
	h := &RequestAuditHandler{store: requestAuditStoreStub{create: func(input requestaudit.CreateBanInput) (*requestaudit.SessionBan, error) {
		require.Equal(t, int64(77), input.CreatedBy)
		return nil, &requestaudit.Error{Status: 503, Code: "BAN_RESULT_UNKNOWN", Message: "Confirm the result", Metadata: map[string]string{"ban_id": testRequestAuditID}}
	}}}
	w := requestAuditTestRequest(requestAuditTestRouter("admin", h.CreateBan), "POST", "", `{"source_audit_id":"`+testRequestAuditID+`","public_reason":"blocked","created_by":999}`)
	require.Equal(t, 503, w.Code)
	require.Contains(t, w.Body.String(), `"reason":"BAN_RESULT_UNKNOWN"`)
	require.Contains(t, w.Body.String(), `"ban_id":"`+testRequestAuditID+`"`)
}

func TestRequestAuditPayloadPreviewPreservesBinaryAndMasksHeaders(t *testing.T) {
	for _, part := range []string{"headers", "body"} {
		t.Run(part, func(t *testing.T) {
			h := &RequestAuditHandler{reads: make(chan struct{}, 2), store: requestAuditStoreStub{read: func(id, gotPart string, offset, limit int64, partial bool) (requestaudit.PayloadResult, error) {
				require.Equal(t, testRequestAuditID, id)
				require.Equal(t, part, gotPart)
				require.Zero(t, offset)
				require.False(t, partial)
				data := []byte{0xff, 0x00, 0xfe}
				if part == "headers" {
					data = []byte(`{"headers":{"Authorization":["Bearer audit-secret"],"X-Value":["a","b"]}}`)
				}
				return requestaudit.PayloadResult{Bytes: data, TotalBytes: int64(len(data)), CaptureState: "complete"}, nil
			}}}
			w := requestAuditTestRequest(requestAuditTestRouter("admin", h.Payload), "GET", "?part="+part, "")
			require.Equal(t, 200, w.Code)
			var envelope struct {
				Data struct {
					Content, Encoding string
					Redacted          bool
				}
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			if part == "headers" {
				require.True(t, envelope.Data.Redacted)
				require.NotContains(t, envelope.Data.Content, "audit-secret")
				require.JSONEq(t, `{"headers":{"Authorization":["[REDACTED]"],"X-Value":["a","b"]}}`, envelope.Data.Content)
			} else {
				require.Equal(t, "base64", envelope.Data.Encoding)
				require.Equal(t, "/wD+", envelope.Data.Content)
			}
		})
	}
}

func TestRequestAuditPayloadRangeAndIncompleteStatus(t *testing.T) {
	data := []byte("0123456789")
	h := &RequestAuditHandler{reads: make(chan struct{}, 2), store: requestAuditStoreStub{read: func(_, _ string, offset, limit int64, partial bool) (requestaudit.PayloadResult, error) {
		if !partial {
			return requestaudit.PayloadResult{}, &requestaudit.Error{Status: 409, Code: "AUDIT_PAYLOAD_INCOMPLETE", Message: "Incomplete capture"}
		}
		return requestaudit.PayloadResult{Bytes: data[offset:min(offset+limit, int64(len(data)))], TotalBytes: int64(len(data)), Partial: true, CaptureState: "partial"}, nil
	}}}
	router := requestAuditTestRouter("admin", h.Payload)
	w := requestAuditTestRequest(router, "GET", "?mode=raw", "")
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), "AUDIT_PAYLOAD_INCOMPLETE")
	req := httptest.NewRequest("GET", "/audit/"+testRequestAuditID+"?mode=raw&allow_partial=true", nil)
	req.Header.Set("Range", "bytes=2-5")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 206, w.Code)
	require.Equal(t, "2345", w.Body.String())
	require.Equal(t, "bytes 2-5/10", w.Header().Get("Content-Range"))
	require.Equal(t, "true", w.Header().Get("X-Audit-Partial"))
	require.Equal(t, "4", w.Header().Get("Content-Length"))
}

func TestRequestAuditRawDownloadAbortsOnLateStorageFailure(t *testing.T) {
	calls := 0
	h := &RequestAuditHandler{reads: make(chan struct{}, 2), store: requestAuditStoreStub{read: func(_, _ string, offset, limit int64, _ bool) (requestaudit.PayloadResult, error) {
		calls++
		if calls == 3 {
			return requestaudit.PayloadResult{}, errors.New("database unavailable")
		}
		return requestaudit.PayloadResult{Bytes: bytes.Repeat([]byte{'x'}, int(limit)), TotalBytes: requestAuditReadChunk + 1, CaptureState: "complete"}, nil
	}}}
	router := requestAuditTestRouter("admin", h.Payload)
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { requestAuditTestRequest(router, "GET", "?mode=raw", "") })
	require.Empty(t, h.reads, "failed downloads must release the read slot")
}

func TestRequestAuditRangeValidation(t *testing.T) {
	for _, tc := range []struct {
		header, limit             string
		offset, total, start, end int64
		invalid                   bool
	}{
		{"", "", 0, 0, 0, 0, false}, {"bytes=-3", "", 0, 10, 7, 10, false},
		{"bytes=2-99", "", 0, 10, 2, 10, false}, {"", "3", 2, 10, 2, 5, false},
		{"bytes=10-", "", 0, 10, 0, 0, true}, {"bytes=0-1,3-4", "", 0, 10, 0, 0, true},
		{"bytes=0-1", "2", 0, 10, 0, 0, true}, {"bytes=0-0", "", 0, 0, 0, 0, true},
		{"", "0", 0, 10, 0, 0, true}, {"", "", 10, 10, 0, 0, true},
	} {
		start, end, _, err := requestAuditRange(tc.header, tc.limit, tc.offset, tc.total)
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.start, start)
		require.Equal(t, tc.end, end)
	}
}

type requestAuditDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *requestAuditDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestRequestAuditDownloadSetsAndResetsLocalWriteDeadline(t *testing.T) {
	h := &RequestAuditHandler{reads: make(chan struct{}, 2), store: requestAuditStoreStub{read: func(_, _ string, _, _ int64, _ bool) (requestaudit.PayloadResult, error) {
		return requestaudit.PayloadResult{Bytes: []byte("x"), TotalBytes: 1, CaptureState: "complete"}, nil
	}}}
	router := requestAuditTestRouter("admin", h.Payload)
	w := &requestAuditDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	started := time.Now()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/audit/"+testRequestAuditID+"?mode=raw", nil))
	require.Equal(t, "x", w.Body.String())
	require.Len(t, w.deadlines, 2)
	require.True(t, w.deadlines[0].After(started))
	require.False(t, w.deadlines[0].After(time.Now().Add(30*time.Second)))
	require.True(t, w.deadlines[1].IsZero(), "the next keepalive request must not inherit the audit deadline")
}

func TestRequestAuditHeaderPreviewRedactsBinaryURI(t *testing.T) {
	data, err := redactRequestAuditHeaders([]byte(`{"request_uri":{"encoding":"base64","content":"Lz92YWx1ZT1zZWNyZXQ="},"headers":{"X-Token":[{"encoding":"base64","content":"c2VjcmV0"}],"X-Normal":["ok"]}}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"request_uri":"[REDACTED]","headers":{"X-Token":["[REDACTED]"],"X-Normal":["ok"]}}`, string(data))
}
