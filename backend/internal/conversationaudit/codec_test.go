package conversationaudit

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type testKeyring struct {
	active string
	keys   map[string][]byte
}

func (k *testKeyring) ActiveKeyID() string { return k.active }
func (k *testKeyring) Key(id string) ([]byte, bool) {
	key, ok := k.keys[id]
	return append([]byte(nil), key...), ok
}

func TestPayloadCodecRoundTripAndRotation(t *testing.T) {
	keys := &testKeyring{active: "v1", keys: map[string][]byte{
		"v1": []byte(strings.Repeat("1", 32)),
		"v2": []byte(strings.Repeat("2", 32)),
	}}
	codec, err := NewPayloadCodec(keys, 1<<20)
	require.NoError(t, err)
	t.Cleanup(codec.Close)
	identity := RecordIdentity{AuditID: uuid.New(), CreatedAt: time.Now()}
	payload := CanonicalConversation{Version: CanonicalVersion, Messages: []Message{{Role: "user", Content: []ContentItem{{Type: "text", Text: strings.Repeat("compress me ", 100)}}}}}

	encodedV1, err := codec.Encode(identity, PayloadSideRequest, payload)
	require.NoError(t, err)
	require.Equal(t, "v1", encodedV1.KeyID)
	require.Less(t, encodedV1.CompressedBytes, encodedV1.OriginalBytes)

	keys.active = "v2"
	encodedV2, err := codec.Encode(identity, PayloadSideResponse, payload)
	require.NoError(t, err)
	require.Equal(t, "v2", encodedV2.KeyID)

	decodedV1, err := codec.Decode(identity, PayloadSideRequest, encodedV1)
	require.NoError(t, err)
	require.Equal(t, payload, decodedV1)
	decodedV2, err := codec.Decode(identity, PayloadSideResponse, encodedV2)
	require.NoError(t, err)
	require.Equal(t, payload, decodedV2)
}

func TestPayloadCodecRejectsTamperAndAADSwaps(t *testing.T) {
	keys := &testKeyring{active: "v1", keys: map[string][]byte{"v1": []byte(strings.Repeat("1", 32))}}
	codec, err := NewPayloadCodec(keys, 1<<20)
	require.NoError(t, err)
	t.Cleanup(codec.Close)
	identity := RecordIdentity{AuditID: uuid.New(), CreatedAt: time.Now()}
	payload := CanonicalConversation{Version: CanonicalVersion, Messages: []Message{}}
	encoded, err := codec.Encode(identity, PayloadSideRequest, payload)
	require.NoError(t, err)

	tampered := encoded
	tampered.Data = append([]byte(nil), encoded.Data...)
	tampered.Data[len(tampered.Data)-1] ^= 1
	_, err = codec.Decode(identity, PayloadSideRequest, tampered)
	require.Error(t, err)
	_, err = codec.Decode(identity, PayloadSideResponse, encoded)
	require.Error(t, err)
	_, err = codec.Decode(RecordIdentity{AuditID: uuid.New(), CreatedAt: identity.CreatedAt}, PayloadSideRequest, encoded)
	require.Error(t, err)
}

func TestPayloadCodecBoundsDecompression(t *testing.T) {
	keys := &testKeyring{active: "v1", keys: map[string][]byte{"v1": []byte(strings.Repeat("1", 32))}}
	codec, err := NewPayloadCodec(keys, MinimumPayloadMaxBytes)
	require.NoError(t, err)
	t.Cleanup(codec.Close)
	identity := RecordIdentity{AuditID: uuid.New(), CreatedAt: time.Now()}
	compressed := codec.encoder.EncodeAll([]byte(strings.Repeat("x", MinimumPayloadMaxBytes*4)), nil)
	block, err := aes.NewCipher(keys.keys["v1"])
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	aad, err := payloadAAD(identity, PayloadSideRequest, PayloadCodecVersion, "v1")
	require.NoError(t, err)
	data := gcm.Seal(nonce, nonce, compressed, aad)

	_, err = codec.Decode(identity, PayloadSideRequest, EncodedPayload{CodecVersion: PayloadCodecVersion, KeyID: "v1", Data: data})
	require.Error(t, err)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}
