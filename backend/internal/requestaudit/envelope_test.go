package requestaudit

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInvalidUTF8HeadersAndRequestTargetRemainLossless(t *testing.T) {
	m := captureTestManager()
	raw := string([]byte{'a', 0xff, 0xfe})
	meta := RequestMeta{Kind: "websocket_handshake", Headers: http.Header{"X-Bytes": {"normal", raw}}, Host: raw, RequestURI: "/v1?raw=" + raw}
	i := BeginIngress(m, meta)
	meta.Headers.Set("X-Bytes", "mutated after ingress")
	i.RecordMessage("text", []byte("message"))
	i.Finish(101, time.Now(), "completed", http.Header{"X-Trailer": {raw}})
	var headerCount, trailerCount int
	for _, event := range takeEvents(m) {
		if event.kind != "chunk" || event.part == PartBody {
			continue
		}
		var envelope map[string]any
		require.NoError(t, json.Unmarshal(event.raw, &envelope))
		headers, ok := envelope["headers"].(map[string]any)
		require.True(t, ok)
		check := func(value any, original string) {
			encoded, ok := value.(map[string]any)
			require.True(t, ok)
			require.Equal(t, "base64", encoded["encoding"])
			content, ok := encoded["content"].(string)
			require.True(t, ok)
			decoded, err := base64.StdEncoding.DecodeString(content)
			require.NoError(t, err)
			require.Equal(t, []byte(original), decoded)
		}
		if event.part == PartHeaders {
			headerCount++
			values, ok := headers["X-Bytes"].([]any)
			require.True(t, ok)
			require.Len(t, values, 2)
			require.Equal(t, "normal", values[0])
			check(values[1], raw)
			check(envelope["host"], raw)
			check(envelope["request_uri"], "/v1?raw="+raw)
		} else {
			trailerCount++
			values, ok := headers["X-Trailer"].([]any)
			require.True(t, ok)
			require.Len(t, values, 1)
			check(values[0], raw)
		}
	}
	require.Equal(t, 2, headerCount, "handshake and each message preserve the same original snapshot")
	require.Equal(t, 1, trailerCount)
}
