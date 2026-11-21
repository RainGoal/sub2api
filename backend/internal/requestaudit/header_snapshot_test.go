package requestaudit

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeaderSnapshotsMaterializeForTheAuditWriter(t *testing.T) {
	request := auditEvent{
		kind: "header_snapshot", part: PartHeaders, seq: 0, payloadEvent: true,
		headerMeta: &RequestMeta{
			Headers: http.Header{"X-Multi": {"one", "two"}},
			Method:  http.MethodPost,
			Host:    "upstream.test",
		},
	}
	require.NoError(t, materializeHeaderSnapshot(&request))
	require.Equal(t, "chunk", request.kind)
	require.False(t, request.payloadEvent)
	require.Contains(t, string(request.raw), `"X-Multi":["one","two"]`)
	require.Nil(t, request.headerMeta)

	upstream := auditEvent{
		kind: "attempt_header_snapshot", part: UpstreamRequestHeaders, payloadEvent: true,
		headerFields: []headerField{{Name: "Authorization", Values: []any{"Bearer original"}}},
	}
	require.NoError(t, materializeHeaderSnapshot(&upstream))
	require.Equal(t, "attempt_chunk", upstream.kind)
	require.False(t, upstream.payloadEvent)
	var fields []headerField
	require.NoError(t, json.Unmarshal(upstream.raw, &fields))
	require.Equal(t, "Bearer original", fields[0].Values[0])
	require.Nil(t, upstream.headerFields)
}
