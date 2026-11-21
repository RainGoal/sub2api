package requestaudit

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditBodyCodecRoundTrip(t *testing.T) {
	codec := &bodyCodec{}
	raw := bytes.Repeat([]byte("\x00\xff text\n"), 8192)
	encoding, packed := codec.encode(raw)
	require.Equal(t, codecZstd, encoding)
	require.Less(t, len(packed), len(raw))
	decoded, err := decodeBlock(encoding, len(raw), packed)
	require.NoError(t, err)
	require.Equal(t, raw, decoded)
	_, err = decodeBlock(encoding, len(raw)-1, packed)
	require.Error(t, err)
	_, err = decodeBlock(encoding, len(raw), packed[:len(packed)/2])
	require.Error(t, err)
	encoding, packed = codec.encode([]byte("short"))
	require.Equal(t, codecRaw, encoding)
	require.Equal(t, []byte("short"), packed)
}

func TestAuditUpstreamBodyQueuePressureDoesNotChangeRead(t *testing.T) {
	m := captureTestManager(WithMaxEvents(2))
	c := &Capture{manager: m}
	c.remaining.Store(256 * 1024)
	part := UpstreamResponseBody
	a := &UpstreamAttempt{capture: c, parts: map[string]*upstreamPartState{part: {}}}
	require.True(t, m.enqueue(auditEvent{kind: "begin"}))
	require.True(t, m.enqueue(auditEvent{kind: "finish"}))
	raw := bytes.Repeat([]byte("data: audit test\n\n"), 8192)
	body := a.WrapBody(part, io.NopCloser(bytes.NewReader(raw)))
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, raw, got)
	require.Equal(t, "queue_full", a.parts[part].reason)
	require.Zero(t, a.parts[part].bytes)
}
