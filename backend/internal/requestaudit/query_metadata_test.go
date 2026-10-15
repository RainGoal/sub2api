package requestaudit

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRecordQueriesReturnMetadataWithoutLosingIDs(t *testing.T) {
	for _, query := range []string{"list", "get"} {
		for _, populated := range []bool{true, false} {
			t.Run(query+"/"+map[bool]string{true: "associated", false: "missing"}[populated], func(t *testing.T) {
				m, mock := mockManager(t)
				id := uuid.NewString()
				var metadata []driver.Value
				if populated {
					metadata = []driver.Value{"alice", "alice@example.com", "Production", "composite", "Engineering"}
				}
				rows := recordRows(id, "complete", "", nil, metadata...)
				var record *Record
				if query == "get" {
					mock.ExpectQuery(regexp.QuoteMeta(withRecordMetadata(recordSelect + ` WHERE id=$1 AND ` + visibleRecordSQL))).
						WithArgs(id).WillReturnRows(rows)
					var err error
					record, err = m.Get(context.Background(), id)
					require.NoError(t, err)
				} else {
					sql := withRecordMetadata(recordSelect+` WHERE `+visibleRecordSQL+` AND user_id = $1 AND api_key_id = $2 ORDER BY received_at DESC,id DESC LIMIT $3`) + ` ORDER BY r.received_at DESC,r.id DESC`
					mock.ExpectQuery(regexp.QuoteMeta(sql)).WithArgs(int64(1), int64(2), 11).WillReturnRows(rows)
					result, err := m.List(context.Background(), ListFilter{UserID: 1, APIKeyID: 2, Limit: 10})
					require.NoError(t, err)
					require.Len(t, result.Items, 1)
					require.False(t, result.HasMore)
					record = result.Items[0]
				}
				require.Equal(t, id, record.ID)
				require.EqualValues(t, 1, record.UserID)
				require.EqualValues(t, 2, record.APIKeyID)
				require.EqualValues(t, 3, record.GroupID)
				require.Equal(t, "codex", record.DeviceType)
				require.Equal(t, "Original-Device", record.DeviceID)
				require.Equal(t, "header", record.DeviceSource)
				require.Equal(t, "resolved", record.DeviceResolution)
				if populated {
					require.Equal(t, "alice", record.UserUsername)
					require.Equal(t, "alice@example.com", record.UserEmail)
					require.Equal(t, "Production", record.APIKeyName)
					require.Equal(t, "composite", record.GroupPlatform)
					require.Equal(t, "Engineering", record.GroupName)
				} else {
					require.Empty(t, record.UserUsername)
					require.Empty(t, record.UserEmail)
					require.Empty(t, record.APIKeyName)
					require.Empty(t, record.GroupPlatform)
					require.Empty(t, record.GroupName)
				}
			})
		}
	}
}
