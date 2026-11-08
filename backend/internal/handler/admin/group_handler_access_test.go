package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupHandlerUpdateAccessGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body string
		wantStatus int
		wantIDs    []int64
	}{
		{"additive grants forwarded", `{"is_exclusive":true,"grant_user_ids":[2,3,2]}`, http.StatusOK, []int64{2, 3, 2}},
		{"omitted compatible", `{}`, http.StatusOK, nil},
		{"empty compatible", `{"grant_user_ids":[]}`, http.StatusOK, []int64{}},
		{"zero rejected", `{"grant_user_ids":[0]}`, http.StatusBadRequest, nil},
		{"negative rejected", `{"grant_user_ids":[-2]}`, http.StatusBadRequest, nil},
		{"invalid type rejected", `{"grant_user_ids":["2"]}`, http.StatusBadRequest, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newStubAdminService()
			h := NewGroupHandler(svc, nil, nil)
			router := gin.New()
			router.PUT("/groups/:id", h.Update)
			request := httptest.NewRequest(http.MethodPut, "/groups/17", bytes.NewBufferString(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.wantStatus, response.Code, response.Body.String())
			if tc.wantStatus == http.StatusOK {
				require.Len(t, svc.updatedGroups, 1)
				require.Equal(t, tc.wantIDs, svc.updatedGroups[0].GrantUserIDs)
			} else {
				require.Empty(t, svc.updatedGroups)
			}
		})
	}
}

type groupAccessRatesHandlerService struct {
	*stubAdminService
	entries []service.UserGroupRateEntry
}

func (s *groupAccessRatesHandlerService) GetGroupRateMultipliers(context.Context, int64) ([]service.UserGroupRateEntry, error) {
	return s.entries, nil
}

func TestGroupHandlerRateMultipliersAccessContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	zero, one, rpm := 0.0, 1.0, 12
	svc := &groupAccessRatesHandlerService{stubAdminService: newStubAdminService(), entries: []service.UserGroupRateEntry{
		{UserID: 2, UserName: "free", UserEmail: "free@example.com", UserStatus: service.StatusActive, RateMultiplier: &zero, HasExplicitAccess: true},
		{UserID: 3, UserStatus: service.StatusActive, RateMultiplier: &one},
		{UserID: 4, UserStatus: service.StatusActive, RPMOverride: &rpm},
	}}
	h := NewGroupHandler(svc, nil, nil)
	router := gin.New()
	router.GET("/groups/:id/rate-multipliers", h.GetGroupRateMultipliers)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/groups/17/rate-multipliers", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":[
		{"user_id":2,"user_name":"free","user_email":"free@example.com","user_notes":"","user_status":"active","rate_multiplier":0,"has_explicit_access":true},
		{"user_id":3,"user_name":"","user_email":"","user_notes":"","user_status":"active","rate_multiplier":1,"has_explicit_access":false},
		{"user_id":4,"user_name":"","user_email":"","user_notes":"","user_status":"active","rpm_override":12,"has_explicit_access":false}
	]}`, response.Body.String())
}
