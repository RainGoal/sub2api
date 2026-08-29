package conversationaudit

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCursorCodecRoundTripAndTamperRejection(t *testing.T) {
	codec, err := NewCursorCodec([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	want := RecordCursor{CreatedAt: time.Now().UTC(), AuditID: uuid.New()}
	encoded, err := codec.Encode(want)
	require.NoError(t, err)
	decoded, err := codec.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, NormalizeCreatedAt(want.CreatedAt), decoded.CreatedAt)
	require.Equal(t, want.AuditID, decoded.AuditID)

	tampered := []byte(encoded)
	tampered[len(tampered)-1] ^= 1
	_, err = codec.Decode(string(tampered))
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = codec.Decode("not-a-cursor")
	require.ErrorIs(t, err, ErrInvalidCursor)
}
