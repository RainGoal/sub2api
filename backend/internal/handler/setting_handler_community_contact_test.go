//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingHandlerCommunityContactPublicJSONContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	empty := `{"group_name":{"zh-CN":"","en-US":""},"description":{"zh-CN":"","en-US":""},"group_number":"","invite_url":"","qr_image_url":""}`
	configured := `{"group_name":{"zh-CN":"测试交流群","en-US":"Test community"},"description":{"zh-CN":"使用交流","en-US":""},"group_number":"10000001","invite_url":"https://community.example/invite","qr_image_url":"/community/qr.png"}`
	for _, tc := range []struct {
		name, stored, want string
	}{
		{"missing", "", empty},
		{"configured", configured, configured},
		{"malformed", `{"invite_url":"javascript:alert(1)"}`, empty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &settingHandlerPublicRepoStub{values: map[string]string{service.SettingKeyCommunityContact: tc.stored}}
			h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test-version")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)
			h.GetPublicSettings(c)
			require.Equal(t, http.StatusOK, rec.Code)
			var response struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Contains(t, response.Data, "community_contact")
			require.JSONEq(t, tc.want, string(response.Data["community_contact"]))
		})
	}
}
