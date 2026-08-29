package conversationaudit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidCursor = errors.New("conversation audit cursor is invalid")

type RecordCursor struct {
	CreatedAt time.Time
	AuditID   uuid.UUID
}

type CursorCodec struct {
	key []byte
}

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("conversation audit cursor key is unavailable")
	}
	return &CursorCodec{key: append([]byte(nil), key...)}, nil
}

func (c *CursorCodec) Encode(cursor RecordCursor) (string, error) {
	if c == nil || len(c.key) < 32 || cursor.CreatedAt.IsZero() || cursor.AuditID == uuid.Nil {
		return "", ErrInvalidCursor
	}
	payload := make([]byte, 24)
	binary.BigEndian.PutUint64(payload[:8], uint64(NormalizeCreatedAt(cursor.CreatedAt).UnixMicro()))
	copy(payload[8:], cursor.AuditID[:])
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	encoded := append(payload, mac.Sum(nil)[:16]...)
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (c *CursorCodec) Decode(value string) (RecordCursor, error) {
	if c == nil || len(c.key) < 32 {
		return RecordCursor{}, ErrInvalidCursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 40 {
		return RecordCursor{}, ErrInvalidCursor
	}
	payload, signature := decoded[:24], decoded[24:]
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)[:16]) {
		return RecordCursor{}, ErrInvalidCursor
	}
	var auditID uuid.UUID
	copy(auditID[:], payload[8:])
	createdAt := time.UnixMicro(int64(binary.BigEndian.Uint64(payload[:8]))).UTC()
	if auditID == uuid.Nil || createdAt.Year() < 2000 || createdAt.Year() > 3000 {
		return RecordCursor{}, ErrInvalidCursor
	}
	return RecordCursor{CreatedAt: createdAt, AuditID: auditID}, nil
}
