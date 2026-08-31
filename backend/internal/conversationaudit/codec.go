package conversationaudit

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

const PayloadCodecVersion int16 = 1

type KeyProvider interface {
	ActiveKeyID() string
	Key(string) ([]byte, bool)
}

type RecordIdentity struct {
	AuditID   uuid.UUID
	CreatedAt time.Time
}

type EncodedPayload struct {
	CodecVersion    int16
	KeyID           string
	Data            []byte
	OriginalBytes   int64
	CompressedBytes int64
	EncryptedBytes  int64
}

type PayloadCodec struct {
	keys       KeyProvider
	encoder    *zstd.Encoder
	decoder    *zstd.Decoder
	random     io.Reader
	maxDecoded int
	closeOnce  sync.Once
}

func NewPayloadCodec(keys KeyProvider, maxDecoded int, concurrency ...int) (*PayloadCodec, error) {
	if keys == nil || keys.ActiveKeyID() == "" {
		return nil, errors.New("conversation audit encryption keyring is unavailable")
	}
	key, ok := keys.Key(keys.ActiveKeyID())
	if !ok || len(key) != 32 {
		return nil, errors.New("conversation audit active encryption key is unavailable")
	}
	if maxDecoded < MinimumPayloadMaxBytes {
		return nil, errors.New("conversation audit decoded size limit is below minimum")
	}
	encoderConcurrency := 2
	if len(concurrency) > 0 {
		encoderConcurrency = concurrency[0]
	}
	if encoderConcurrency < 1 || encoderConcurrency > 8 {
		return nil, errors.New("conversation audit codec concurrency is invalid")
	}
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(encoderConcurrency),
	)
	if err != nil {
		return nil, fmt.Errorf("create conversation audit zstd encoder: %w", err)
	}
	decoder, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(uint64(maxDecoded)),
		zstd.WithDecoderMaxWindow(uint64(maxDecoded)),
	)
	if err != nil {
		_ = encoder.Close()
		return nil, fmt.Errorf("create conversation audit zstd decoder: %w", err)
	}
	return &PayloadCodec{keys: keys, encoder: encoder, decoder: decoder, random: rand.Reader, maxDecoded: maxDecoded}, nil
}

func (c *PayloadCodec) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.encoder != nil {
			_ = c.encoder.Close()
		}
		if c.decoder != nil {
			c.decoder.Close()
		}
	})
}

func (c *PayloadCodec) Encode(identity RecordIdentity, side PayloadSide, payload CanonicalConversation) (EncodedPayload, error) {
	if c == nil || c.encoder == nil || c.keys == nil {
		return EncodedPayload{}, errors.New("conversation audit payload codec is unavailable")
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return EncodedPayload{}, fmt.Errorf("marshal conversation audit payload: %w", err)
	}
	if len(plain) > c.maxDecoded {
		return EncodedPayload{}, errors.New("conversation audit payload exceeds decoded size limit")
	}
	compressed := c.encoder.EncodeAll(plain, nil)
	keyID := c.keys.ActiveKeyID()
	key, ok := c.keys.Key(keyID)
	if !ok || len(key) != 32 {
		return EncodedPayload{}, errors.New("conversation audit active encryption key is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return EncodedPayload{}, fmt.Errorf("create conversation audit cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncodedPayload{}, fmt.Errorf("create conversation audit gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return EncodedPayload{}, fmt.Errorf("generate conversation audit nonce: %w", err)
	}
	aad, err := payloadAAD(identity, side, PayloadCodecVersion, keyID)
	if err != nil {
		return EncodedPayload{}, err
	}
	encrypted := gcm.Seal(nonce, nonce, compressed, aad)
	return EncodedPayload{
		CodecVersion: PayloadCodecVersion,
		KeyID:        keyID, Data: encrypted,
		OriginalBytes: int64(len(plain)), CompressedBytes: int64(len(compressed)), EncryptedBytes: int64(len(encrypted)),
	}, nil
}

func (c *PayloadCodec) Decode(identity RecordIdentity, side PayloadSide, encoded EncodedPayload) (CanonicalConversation, error) {
	if c == nil || c.decoder == nil || c.keys == nil {
		return CanonicalConversation{}, errors.New("conversation audit payload codec is unavailable")
	}
	if encoded.CodecVersion != PayloadCodecVersion {
		return CanonicalConversation{}, errors.New("conversation audit payload codec version is unsupported")
	}
	key, ok := c.keys.Key(encoded.KeyID)
	if !ok || len(key) != 32 {
		return CanonicalConversation{}, errors.New("conversation audit payload key is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return CanonicalConversation{}, fmt.Errorf("create conversation audit cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return CanonicalConversation{}, fmt.Errorf("create conversation audit gcm: %w", err)
	}
	if len(encoded.Data) < gcm.NonceSize()+gcm.Overhead() {
		return CanonicalConversation{}, errors.New("conversation audit ciphertext is invalid")
	}
	nonce := encoded.Data[:gcm.NonceSize()]
	ciphertext := encoded.Data[gcm.NonceSize():]
	aad, err := payloadAAD(identity, side, encoded.CodecVersion, encoded.KeyID)
	if err != nil {
		return CanonicalConversation{}, err
	}
	compressed, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return CanonicalConversation{}, errors.New("conversation audit payload authentication failed")
	}
	plain, err := c.decoder.DecodeAll(compressed, nil)
	if err != nil {
		return CanonicalConversation{}, fmt.Errorf("conversation audit payload decompression failed: %w", err)
	}
	if len(plain) > c.maxDecoded {
		return CanonicalConversation{}, errors.New("conversation audit decoded payload exceeds limit")
	}
	var payload CanonicalConversation
	if err := json.Unmarshal(plain, &payload); err != nil {
		return CanonicalConversation{}, errors.New("conversation audit decoded payload is invalid")
	}
	if payload.Version != CanonicalVersion {
		return CanonicalConversation{}, errors.New("conversation audit canonical payload version is unsupported")
	}
	return payload, nil
}

func payloadAAD(identity RecordIdentity, side PayloadSide, version int16, keyID string) ([]byte, error) {
	if identity.AuditID == uuid.Nil {
		return nil, errors.New("conversation audit identity is invalid")
	}
	if side != PayloadSideRequest && side != PayloadSideResponse {
		return nil, errors.New("conversation audit payload side is invalid")
	}
	if len(keyID) < 1 || len(keyID) > 64 {
		return nil, errors.New("conversation audit payload key id is invalid")
	}
	aad := make([]byte, 0, 16+8+1+2+1+len(keyID))
	aad = append(aad, identity.AuditID[:]...)
	timestamp := make([]byte, 8)
	binary.BigEndian.PutUint64(timestamp, uint64(identity.CreatedAt.UTC().Truncate(time.Microsecond).UnixMicro()))
	aad = append(aad, timestamp...)
	if side == PayloadSideRequest {
		aad = append(aad, 1)
	} else {
		aad = append(aad, 2)
	}
	codec := make([]byte, 2)
	binary.BigEndian.PutUint16(codec, uint16(version))
	aad = append(aad, codec...)
	aad = append(aad, byte(len(keyID)))
	aad = append(aad, keyID...)
	return aad, nil
}
