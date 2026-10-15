package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type requestAuditDeviceStoreStub struct {
	RequestAuditAdminStore
	list   func(requestaudit.DeviceBanFilter) (requestaudit.DeviceBanList, error)
	get    func(string) (*requestaudit.DeviceBan, error)
	create func(requestaudit.CreateDeviceBanInput) (*requestaudit.DeviceBan, error)
	revoke func(string, int64) (*requestaudit.DeviceBan, error)
}

func (s requestAuditDeviceStoreStub) ListDeviceBans(_ context.Context, filter requestaudit.DeviceBanFilter) (requestaudit.DeviceBanList, error) {
	return s.list(filter)
}
func (s requestAuditDeviceStoreStub) GetDeviceBan(_ context.Context, id string) (*requestaudit.DeviceBan, error) {
	return s.get(id)
}
func (s requestAuditDeviceStoreStub) CreateDeviceBan(_ context.Context, input requestaudit.CreateDeviceBanInput) (*requestaudit.DeviceBan, error) {
	return s.create(input)
}
func (s requestAuditDeviceStoreStub) RevokeDeviceBan(_ context.Context, id string, actor int64) (*requestaudit.DeviceBan, error) {
	return s.revoke(id, actor)
}

func TestRequestAuditDeviceBanUsesAuditIdentityAndAuthenticatedActor(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	h := &RequestAuditHandler{store: requestAuditDeviceStoreStub{create: func(input requestaudit.CreateDeviceBanInput) (*requestaudit.DeviceBan, error) {
		require.Equal(t, testRequestAuditID, input.SourceAuditID)
		require.Equal(t, int64(77), input.CreatedBy)
		require.Equal(t, "blocked", input.PublicReason)
		require.Equal(t, "reviewed", input.InternalNote)
		require.Equal(t, expires, *input.ExpiresAt)
		return &requestaudit.DeviceBan{ID: testRequestAuditID, DeviceType: "codex", DeviceID: "raw-installation-id", DeviceSource: "header:x-codex-turn-metadata.installation_id", CreatedBy: input.CreatedBy}, nil
	}}}
	w := requestAuditTestRequest(requestAuditTestRouter("admin", h.CreateDeviceBan), http.MethodPost, "", `{"source_audit_id":"`+testRequestAuditID+`","public_reason":"blocked","internal_note":"reviewed","expires_at":"`+expires.Format(time.RFC3339)+`","created_by":999,"device_type":"claude","device_id":"forged-id"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var envelope struct {
		Data requestaudit.DeviceBan `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, "codex", envelope.Data.DeviceType)
	require.Equal(t, "raw-installation-id", envelope.Data.DeviceID)
	require.Equal(t, int64(77), envelope.Data.CreatedBy)
}

func TestRequestAuditDeviceBanErrorsPreserveReasonAndResultID(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"BAN_IDENTITY_UNAVAILABLE", http.StatusConflict},
		{"DEVICE_ALREADY_BANNED", http.StatusConflict},
		{"BAN_RESULT_UNKNOWN", http.StatusServiceUnavailable},
	} {
		t.Run(tc.code, func(t *testing.T) {
			h := &RequestAuditHandler{store: requestAuditDeviceStoreStub{create: func(requestaudit.CreateDeviceBanInput) (*requestaudit.DeviceBan, error) {
				return nil, &requestaudit.Error{Status: tc.status, Code: tc.code, Message: "Review device ban", Metadata: map[string]string{"ban_id": testRequestAuditID}}
			}}}
			w := requestAuditTestRequest(requestAuditTestRouter("admin", h.CreateDeviceBan), http.MethodPost, "", `{"source_audit_id":"`+testRequestAuditID+`","public_reason":"blocked"}`)
			require.Equal(t, tc.status, w.Code)
			require.Contains(t, w.Body.String(), `"reason":"`+tc.code+`"`)
			require.Contains(t, w.Body.String(), `"ban_id":"`+testRequestAuditID+`"`)
		})
	}
}

func TestRequestAuditDeviceBanValidationDoesNotCallStorage(t *testing.T) {
	h := &RequestAuditHandler{store: requestAuditDeviceStoreStub{}}
	for name, handle := range map[string]gin.HandlerFunc{"get": h.GetDeviceBan, "revoke": h.RevokeDeviceBan} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			requestAuditTestRouter("admin", handle).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/audit/invalid-id", nil))
			require.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
	for _, body := range []string{`{`, `{}`, `{"source_audit_id":"invalid"}`} {
		require.Equal(t, http.StatusBadRequest, requestAuditTestRequest(requestAuditTestRouter("admin", h.CreateDeviceBan), http.MethodPost, "", body).Code)
	}
	for _, query := range []string{"?limit=101", "?limit=0", "?state=deleted"} {
		require.Equal(t, http.StatusBadRequest, requestAuditTestRequest(requestAuditTestRouter("admin", h.ListDeviceBans), http.MethodGet, query, "").Code)
	}
}

func TestRequestAuditDeviceBanListAndRevoke(t *testing.T) {
	h := &RequestAuditHandler{store: requestAuditDeviceStoreStub{
		list: func(filter requestaudit.DeviceBanFilter) (requestaudit.DeviceBanList, error) {
			require.Equal(t, requestaudit.DeviceBanFilter{DeviceType: "codex", DeviceID: "original-ID", SourceAuditID: testRequestAuditID, State: "active", Cursor: "page-two", Limit: 25}, filter)
			return requestaudit.DeviceBanList{Items: []*requestaudit.DeviceBan{}, NextCursor: "next", HasMore: true}, nil
		},
		revoke: func(id string, actor int64) (*requestaudit.DeviceBan, error) {
			require.Equal(t, testRequestAuditID, id)
			require.Equal(t, int64(77), actor)
			return &requestaudit.DeviceBan{ID: id, DeviceType: "codex", DeviceID: "original-ID", State: "revoked", RevokedBy: &actor}, nil
		},
	}}
	w := requestAuditTestRequest(requestAuditTestRouter("admin", h.ListDeviceBans), http.MethodGet, "?device_type=codex&device_id=original-ID&source_audit_id="+testRequestAuditID+"&state=active&cursor=page-two&limit=25", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"items":[],"next_cursor":"next","has_more":true}}`, w.Body.String())
	w = requestAuditTestRequest(requestAuditTestRouter("admin", h.RevokeDeviceBan), http.MethodPost, "", `{"revoked_by":999}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"state":"revoked"`)
	require.Contains(t, w.Body.String(), `"revoked_by":77`)
}

func TestRequestAuditListForwardsExactDeviceFilters(t *testing.T) {
	store := &requestAuditListFilterStore{}
	h := &RequestAuditHandler{store: store}
	w := requestAuditTestRequest(requestAuditTestRouter("admin", h.List), http.MethodGet, "?device_type=claude&device_id=Case-Sensitive-ID", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "claude", store.filter.DeviceType)
	require.Equal(t, "Case-Sensitive-ID", store.filter.DeviceID)
}

func TestRequestAuditConfigPreservesDeviceBlockingForOlderClients(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "version_conflict"}[conflict], func(t *testing.T) {
			updated := false
			h := &RequestAuditHandler{store: requestAuditStoreStub{
				config: func() (requestaudit.Config, error) {
					version := int64(1)
					if conflict {
						version = 2
					}
					return requestaudit.Config{DeviceBanEnabled: true, Version: version}, nil
				},
				update: func(input requestaudit.ConfigUpdate) (requestaudit.Config, error) {
					updated = true
					require.True(t, input.DeviceBanEnabled)
					require.Equal(t, int64(1), input.ExpectedVersion, "preserving the device switch must not replace the client's version")
					return requestaudit.Config{DeviceBanEnabled: input.DeviceBanEnabled, Version: 2}, nil
				},
			}}
			w := requestAuditTestRequest(requestAuditTestRouter("admin", h.UpdateConfig), http.MethodPut, "", `{"enabled":false,"session_ban_enabled":false,"retention_days":3,"expected_version":1}`)
			if conflict {
				require.Equal(t, http.StatusConflict, w.Code)
				require.Contains(t, w.Body.String(), "CONFIG_VERSION_CONFLICT")
				require.False(t, updated, "a cached config from another version must not supply the omitted device switch")
			} else {
				require.Equal(t, http.StatusOK, w.Code)
				require.Contains(t, w.Body.String(), `"device_ban_enabled":true`)
				require.True(t, updated)
			}
		})
	}
}

func TestRequestAuditConfigAllowsExplicitDeviceBlockingDisable(t *testing.T) {
	h := &RequestAuditHandler{store: requestAuditStoreStub{update: func(input requestaudit.ConfigUpdate) (requestaudit.Config, error) {
		require.False(t, input.DeviceBanEnabled)
		return requestaudit.Config{}, nil
	}}}
	w := requestAuditTestRequest(requestAuditTestRouter("admin", h.UpdateConfig), http.MethodPut, "", `{"enabled":false,"session_ban_enabled":false,"device_ban_enabled":false,"retention_days":3,"expected_version":1}`)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequestAuditConfigRejectsOmittedDeviceSwitchFromStaleCache(t *testing.T) {
	h := &RequestAuditHandler{store: requestAuditStoreStub{config: func() (requestaudit.Config, error) {
		return requestaudit.Config{DeviceBanEnabled: false, Version: 1}, nil
	}}}
	// The client read version 2 (with blocking enabled) from another instance.
	// This instance must not silently copy its stale disabled switch into it.
	w := requestAuditTestRequest(requestAuditTestRouter("admin", h.UpdateConfig), http.MethodPut, "", `{"enabled":false,"session_ban_enabled":false,"retention_days":3,"expected_version":2}`)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "CONFIG_VERSION_CONFLICT")
}
