package admin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type requestAuditMetadataStore struct {
	RequestAuditAdminStore
	record *requestaudit.Record
}

func (s requestAuditMetadataStore) List(context.Context, requestaudit.ListFilter) (requestaudit.ListResult, error) {
	return requestaudit.ListResult{Items: []*requestaudit.Record{s.record}}, nil
}

func (s requestAuditMetadataStore) Get(context.Context, string) (*requestaudit.Record, error) {
	return s.record, nil
}

type requestAuditListFilterStore struct {
	RequestAuditAdminStore
	filter requestaudit.ListFilter
}

func (s *requestAuditListFilterStore) List(_ context.Context, filter requestaudit.ListFilter) (requestaudit.ListResult, error) {
	s.filter = filter
	return requestaudit.ListResult{Items: []*requestaudit.Record{}}, nil
}

func TestRequestAuditSessionFilterAllowsOptionalAPIKey(t *testing.T) {
	for _, tc := range []struct {
		query string
		key   int64
	}{
		{"?session_id=shared-session", 0},
		{"?session_id=shared-session&api_key_id=7", 7},
	} {
		t.Run(tc.query, func(t *testing.T) {
			store := &requestAuditListFilterStore{}
			h := &RequestAuditHandler{store: store}
			w := requestAuditTestRequest(requestAuditTestRouter("admin", h.List), "GET", tc.query, "")
			require.Equal(t, 200, w.Code)
			require.Equal(t, "shared-session", store.filter.SessionID)
			require.Equal(t, tc.key, store.filter.APIKeyID)
			require.Equal(t, 50, store.filter.Limit)
		})
	}
}

func TestRequestAuditDisplayMetadataResponseContract(t *testing.T) {
	for _, populated := range []bool{true, false} {
		record := &requestaudit.Record{ID: testRequestAuditID, Identity: requestaudit.Identity{UserID: 1, APIKeyID: 2, GroupID: 3}}
		if populated {
			record.UserUsername, record.UserEmail = "alice", "alice@example.com"
			record.APIKeyName, record.GroupPlatform = "Production", "composite"
			record.GroupName = "Engineering"
		}
		h := &RequestAuditHandler{store: requestAuditMetadataStore{record: record}}
		for name, handle := range map[string]gin.HandlerFunc{"list": h.List, "get": h.Get} {
			t.Run(name+"/"+map[bool]string{true: "associated", false: "missing"}[populated], func(t *testing.T) {
				w := requestAuditTestRequest(requestAuditTestRouter("admin", handle), "GET", "", "")
				require.Equal(t, 200, w.Code)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				var response struct {
					Code int             `json:"code"`
					Data json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				require.Zero(t, response.Code)
				data := response.Data
				if name == "list" {
					var list struct {
						Items []json.RawMessage `json:"items"`
					}
					require.NoError(t, json.Unmarshal(data, &list))
					require.Len(t, list.Items, 1)
					data = list.Items[0]
				}
				var item map[string]any
				require.NoError(t, json.Unmarshal(data, &item))
				for field, want := range map[string]any{
					"id": testRequestAuditID, "user_id": float64(1), "api_key_id": float64(2), "group_id": float64(3),
					"user_username": record.UserUsername, "user_email": record.UserEmail,
					"api_key_name": record.APIKeyName, "group_platform": record.GroupPlatform, "group_name": record.GroupName,
				} {
					require.Equal(t, want, item[field], field)
				}
				require.NotContains(t, item, "key")
				require.NotContains(t, item, "platform", "the response must identify this as group metadata")
			})
		}
	}
}
