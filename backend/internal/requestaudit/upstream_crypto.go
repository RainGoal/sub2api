package requestaudit

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

func WithDerivedUpstreamEncryptionKey(key string) (Option, error) {
	raw, err := hex.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("TOTP_ENCRYPTION_KEY must be 64 hexadecimal characters")
	}
	mac := hmac.New(sha256.New, raw)
	_, _ = mac.Write([]byte("sub2api/request-audit/upstream/v1"))
	derived := mac.Sum(nil)
	clear(raw)
	block, err := aes.NewCipher(derived)
	clear(derived)
	if err != nil {
		return nil, err
	}
	ciphertext, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return func(m *Manager) { m.upstreamCrypto = ciphertext }, nil
}

func (m *Manager) UpstreamCaptureAvailable() bool {
	return m != nil && m.upstreamCrypto != nil
}

func upstreamAAD(id string, attempt int, part string, seq, codec, size int) []byte {
	return fmt.Appendf(nil, "%s/%d/%s/%d/%d/%d", id, attempt, part, seq, codec, size)
}

func (m *Manager) sealUpstream(e auditEvent) ([]byte, error) {
	if !m.UpstreamCaptureAvailable() {
		return nil, errors.New("request audit upstream encryption unavailable")
	}
	// NewGCMWithRandomNonce creates a fresh nonce for every Seal call.
	// #nosec G407 -- the API intentionally accepts a nil nonce when configured with NewGCMWithRandomNonce.
	return m.upstreamCrypto.Seal(nil, nil, eventPayload(e),
		upstreamAAD(e.id, e.attemptNo, e.part, e.seq, eventCodec(e), len(e.raw))), nil
}

func (m *Manager) openUpstream(id string, attempt int, part string, seq, codec, size int, payload []byte) ([]byte, error) {
	if !m.UpstreamCaptureAvailable() {
		return nil, errors.New("request audit upstream encryption unavailable")
	}
	cleartext, err := m.upstreamCrypto.Open(nil, nil, payload, upstreamAAD(id, attempt, part, seq, codec, size))
	if err != nil {
		return nil, err
	}
	return decodeBlock(codec, size, cleartext)
}
