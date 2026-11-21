package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/stretchr/testify/require"
)

type requestAuditGroupPolicyStoreStub struct {
	RequestAuditAdminStore
	config requestaudit.GroupPolicyConfig
}

func (s *requestAuditGroupPolicyStoreStub) GetGroupPolicies(context.Context) (requestaudit.GroupPolicyConfig, error) {
	return s.config, nil
}

func (s *requestAuditGroupPolicyStoreStub) UpdateGroupPolicies(_ context.Context, next requestaudit.GroupPolicyConfig, expected int64) (requestaudit.GroupPolicyConfig, error) {
	if expected != s.config.Version {
		return requestaudit.GroupPolicyConfig{}, &requestaudit.Error{Status: http.StatusConflict, Code: "CONFIG_VERSION_CONFLICT"}
	}
	next.Version = expected + 1
	s.config = next
	return next, nil
}

func TestRequestAuditGroupPolicyEndpoints(t *testing.T) {
	store := &requestAuditGroupPolicyStoreStub{config: requestaudit.GroupPolicyConfig{Rules: []requestaudit.GroupPolicy{}, Version: 1}}
	handler := &RequestAuditHandler{store: store}
	read := requestAuditTestRequest(requestAuditTestRouter("admin", handler.GetGroupPolicies), http.MethodGet, "", "")
	require.Equal(t, http.StatusOK, read.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"group_only":false,"rules":[],"version":1}}`, read.Body.String())
	const update = `{"group_only":true,"rules":[{"group_id":4,"upstream_request_headers":true}],"expected_version":1}`
	w := requestAuditTestRequest(requestAuditTestRouter("user", handler.UpdateGroupPolicies), http.MethodPut, "", update)
	require.Equal(t, http.StatusForbidden, w.Code)
	router := requestAuditTestRouter("admin", handler.UpdateGroupPolicies)
	for _, body := range []string{
		`{"rules":[],"expected_version":1}`,
		`{"group_only":true,"expected_version":1}`,
		`{"group_only":true,"rules":[]}`,
	} {
		w = requestAuditTestRequest(router, http.MethodPut, "", body)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	w = requestAuditTestRequest(router, http.MethodPut, "", update)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, requestaudit.GroupPolicyConfig{GroupOnly: true, Rules: []requestaudit.GroupPolicy{{GroupID: 4, UpstreamRequestHeaders: true}}, Version: 2}, store.config)
	w = requestAuditTestRequest(router, http.MethodPut, "", update)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "CONFIG_VERSION_CONFLICT")
}
