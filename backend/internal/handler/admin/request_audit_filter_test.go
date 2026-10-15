package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestAuditGroupFilter(t *testing.T) {
	for _, tc := range []struct {
		query  string
		group  int64
		status int
	}{
		{"", 0, http.StatusOK},
		{"?group_id=", 0, http.StatusOK},
		{"?group_id=42", 42, http.StatusOK},
		{"?group_id=42&api_key_id=7&session_id=session", 42, http.StatusOK},
		{"?group_id=0", 0, http.StatusBadRequest},
		{"?group_id=-1", 0, http.StatusBadRequest},
		{"?group_id=invalid", 0, http.StatusBadRequest},
		{"?group_id=9223372036854775808", 0, http.StatusBadRequest},
	} {
		t.Run(tc.query, func(t *testing.T) {
			store := &requestAuditListFilterStore{}
			h := &RequestAuditHandler{store: store}
			w := requestAuditTestRequest(requestAuditTestRouter("admin", h.List), http.MethodGet, tc.query, "")
			require.Equal(t, tc.status, w.Code)
			if tc.status == http.StatusOK {
				require.Equal(t, tc.group, store.filter.GroupID)
			} else {
				require.Contains(t, w.Body.String(), "INVALID_ARGUMENT")
				require.Zero(t, store.filter.Limit, "invalid filters must not query storage")
			}
		})
	}
}

func TestRequestAuditRequestIDFilter(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		status      int
	}{
		{"", "", http.StatusOK},
		{"  ", "", http.StatusOK},
		{" client:Original-ID ", "client:Original-ID", http.StatusOK},
		{"resp_caseSensitive", "resp_caseSensitive", http.StatusOK},
		{"raw-client-id", "raw-client-id", http.StatusOK},
		{strings.Repeat("x", 256), strings.Repeat("x", 256), http.StatusOK},
		{strings.Repeat("x", 257), "", http.StatusBadRequest},
		{strings.Repeat("界", 86), "", http.StatusBadRequest},
		{"bad\x00id", "", http.StatusBadRequest},
		{"bad\nid", "", http.StatusBadRequest},
		{"bad\x7fid", "", http.StatusBadRequest},
		{"bad\xffid", "", http.StatusBadRequest},
	} {
		t.Run(tc.input, func(t *testing.T) {
			store := &requestAuditListFilterStore{}
			h := &RequestAuditHandler{store: store}
			query := "?request_id=" + url.QueryEscape(tc.input) + "&group_id=42"
			w := requestAuditTestRequest(requestAuditTestRouter("admin", h.List), http.MethodGet, query, "")
			require.Equal(t, tc.status, w.Code)
			if tc.status == http.StatusOK {
				require.Equal(t, tc.want, store.filter.RequestID)
				require.Equal(t, int64(42), store.filter.GroupID)
			} else {
				require.Contains(t, w.Body.String(), "INVALID_ARGUMENT")
				require.Zero(t, store.filter.Limit)
			}
		})
	}
}
