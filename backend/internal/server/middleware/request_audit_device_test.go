package middleware

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newRequestAuditDevicePolicy(t *testing.T, enabled bool) (*requestaudit.Manager, sqlmock.Sqlmock) {
	cfg := requestaudit.DefaultConfig()
	cfg.DeviceBanEnabled = enabled
	return newRequestAuditDevicePolicyConfig(t, cfg)
}

func newRequestAuditDevicePolicyConfig(t *testing.T, cfg requestaudit.Config) (*requestaudit.Manager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	value, err := json.Marshal(cfg)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT value FROM settings`).WithArgs(requestaudit.ConfigKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(value)))
	manager := requestaudit.NewManager(db)
	require.NoError(t, manager.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, manager.Stop(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return manager, mock
}

func requestAuditDeviceBanRows(deviceType, device string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "device_type", "device_id", "public_reason", "internal_note", "source_audit_id", "device_source", "created_by", "created_at", "expires_at", "revoked_by", "revoked_at"}).
		AddRow("device-ban", deviceType, device, "Device public reason", "private device note", nil, "inbound", int64(1), time.Now(), nil, nil, nil)
}

func TestRequestAuditDeviceHeaderBanAcrossKeysBeforeBody(t *testing.T) {
	for _, key := range []int64{12, 99} {
		manager, mock := newRequestAuditDevicePolicy(t, true)
		mock.ExpectQuery(`FROM device_bans`).WithArgs("codex", "Original-Device").WillReturnRows(requestAuditDeviceBanRows("codex", "Original-Device"))
		router := gin.New()
		router.Use(RequestAuditBegin(manager), func(c *gin.Context) {
			c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: key, UserID: key + 1})
			c.Next()
		}, RequestAuditAfterAuth(""))
		router.POST("/v1/responses", func(c *gin.Context) { t.Error("banned device reached handler") })
		body := bytes.NewBufferString(`{"client_metadata":{"session_id":"new-session"}}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
		req.Header.Set("X-Codex-Turn-Metadata", `{"installation_id":"Original-Device"}`)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Contains(t, w.Body.String(), "device_banned")
		require.NotContains(t, w.Body.String(), "private device note")
		require.NotEmpty(t, body.String(), "header rejection must not consume body")
		require.Zero(t, manager.Status().AcceptedRequests)
	}
}

func TestRequestAuditDeviceBodyBanBeforeRewrite(t *testing.T) {
	for _, tc := range []struct {
		path, deviceType, body string
		gzip                   bool
	}{
		{"/v1/messages", "claude", `{"metadata":{"user_id":"{\"device_id\":\"Original-Device\"}"}}`, false},
		{"/v1/messages", "claude", `{"metadata":{"user_id":"{\"device_id\":\"Original-Device\",\"session_id\":\"new-session\"}"}}`, true},
		{"/v1/responses", "codex", `{"client_metadata":{"x-codex-installation-id":"Original-Device"}}`, false},
		{"/v1/responses", "codex", "\xef\xbb\xbf" + `{"client_metadata":{"x-codex-turn-metadata":"{\"installation_id\":\"Original-Device\"}"}}`, false},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			manager, mock := newRequestAuditDevicePolicy(t, true)
			mock.ExpectQuery(`FROM device_bans`).WithArgs(tc.deviceType, "Original-Device").WillReturnRows(requestAuditDeviceBanRows(tc.deviceType, "Original-Device"))
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth("", 4096))
			router.POST(tc.path, func(c *gin.Context) {
				_, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.ErrorIs(t, err, httputil.ErrBodyObserverRejected)
				identity := requestaudit.IngressFromContext(c.Request.Context()).Identity()
				require.Equal(t, "Original-Device", identity.DeviceID)
				require.Equal(t, tc.deviceType, identity.DeviceType)
			})
			var raw bytes.Buffer
			if tc.gzip {
				writer := gzip.NewWriter(&raw)
				_, err := writer.Write([]byte(tc.body))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
			} else {
				_, err := raw.WriteString(tc.body)
				require.NoError(t, err)
			}
			req := httptest.NewRequest(http.MethodPost, tc.path, &raw)
			if tc.deviceType == "claude" {
				req.Header.Set("X-Codex-Installation-Id", "unrelated-codex-installation")
			}
			if tc.gzip {
				req.Header.Set("Content-Encoding", "gzip")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Contains(t, w.Body.String(), "Device public reason")
			require.NotContains(t, w.Body.String(), "private device note")
		})
	}
}

func TestRequestAuditDeviceAdmissionPreservesRawIdentityAndRunsOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		failure error
	}{
		{"disabled", false, nil},
		{"allowed", true, sql.ErrNoRows},
		{"lookup failure", true, errors.New("policy unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, mock := newRequestAuditDevicePolicy(t, tc.enabled)
			if tc.enabled {
				mock.ExpectQuery(`FROM device_bans`).WithArgs("codex", "Original-Device").WillReturnError(tc.failure)
			}
			router := gin.New()
			router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth("", 4096), RequestAuditAfterRouting(""))
			const original = `{"client_metadata":{"x-codex-installation-id":"Body-Device"}}`
			router.POST("/v1/responses", func(c *gin.Context) {
				body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err)
				require.Equal(t, original, string(body))
				// Simulate provider-side fingerprint rewriting and a retry read.
				c.Request.Header.Set("X-Codex-Installation-Id", "Upstream-Account-Device")
				c.Request.Body = httputil.NewPrereadBody(body)
				_, err = httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err)
				identity := requestaudit.IngressFromContext(c.Request.Context()).Identity()
				if tc.enabled {
					require.Equal(t, "Original-Device", identity.DeviceID)
				} else {
					require.Empty(t, identity.DeviceID)
				}
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(original))
			req.Header.Set("X-Codex-Installation-Id", "Original-Device")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code)
		})
	}
}

func TestRequestAuditUnusedDeviceTrackingPreservesBodyAndSSE(t *testing.T) {
	for _, sessionOnly := range []bool{false, true} {
		for _, path := range []string{"/v1/messages", "/v1/responses"} {
			t.Run(path+map[bool]string{false: "/all_off", true: "/session_only"}[sessionOnly], func(t *testing.T) {
				cfg := requestaudit.DefaultConfig()
				cfg.SessionBanEnabled = sessionOnly
				manager, mock := newRequestAuditDevicePolicyConfig(t, cfg)
				if sessionOnly {
					mock.ExpectQuery(`FROM session_bans`).WithArgs(int64(12), "original-session").WillReturnError(sql.ErrNoRows)
				}
				const original = `{"metadata":{"user_id":"{\"device_id\":\"Claude-Device\",\"session_id\":\"body-session\"}"},"client_metadata":{"x-codex-installation-id":"Body-Device"},"input":"unchanged"}`
				router := gin.New()
				router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth("", 4096), RequestAuditAfterRouting(""))
				router.POST(path, func(c *gin.Context) {
					body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
					require.NoError(t, err)
					require.Equal(t, original, string(body))
					identity := requestaudit.IngressFromContext(c.Request.Context()).Identity()
					require.Empty(t, identity.DeviceType)
					require.Empty(t, identity.DeviceID)
					require.Empty(t, identity.DeviceSource)
					require.Empty(t, identity.DeviceResolution)
					require.Equal(t, "original-session", identity.SessionID)
					c.Header("Content-Type", "text/event-stream")
					_, err = c.Writer.Write(append([]byte("data: "), body...))
					require.NoError(t, err)
					c.Writer.Flush()
				})
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(original))
				req.Header.Set("session-id", "original-session")
				req.Header.Set("X-Codex-Installation-Id", "Header-Device")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, http.StatusOK, w.Code)
				require.Equal(t, "data: "+original, w.Body.String())
				require.True(t, w.Flushed)
				require.Zero(t, manager.Status().AcceptedRequests)
			})
		}
	}
}

func TestRequestAuditCaptureTracksDeviceWhileDevicePolicyIsDisabled(t *testing.T) {
	cfg := requestaudit.DefaultConfig()
	cfg.Enabled = true
	manager, mock := newRequestAuditDevicePolicyConfig(t, cfg)
	// A capture outage must not affect the admitted HTTP request or its body.
	mock.ExpectBegin().WillReturnError(errors.New("capture test writer unavailable"))
	const original = `{"client_metadata":{"x-codex-installation-id":"Body-Device"},"input":"unchanged"}`
	router := gin.New()
	router.Use(RequestAuditBegin(manager), requestAuditTestAuth, RequestAuditAfterAuth("", 4096))
	router.POST("/v1/responses", func(c *gin.Context) {
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		require.Equal(t, original, string(body))
		identity := requestaudit.IngressFromContext(c.Request.Context()).Identity()
		require.Equal(t, "codex", identity.DeviceType)
		require.Equal(t, "Body-Device", identity.DeviceID)
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(original)))
	require.Equal(t, http.StatusNoContent, w.Code)
	require.EqualValues(t, 1, manager.Status().AcceptedRequests)
}
