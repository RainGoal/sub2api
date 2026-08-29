package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const maxConversationAuditKeys = 32

// ConversationAuditConfig contains deployment-owned encryption material only.
// Capture policy and retention are administrator settings stored in PostgreSQL.
type ConversationAuditConfig struct {
	ActiveKeyID string `mapstructure:"active_key_id"`
	// Keyring is a comma, semicolon, or newline separated list of id=key pairs.
	// Key material may be 64-character hex, hex:<value>, or base64:<value>.
	Keyring string `mapstructure:"keyring"`
}

type ConversationAuditKeyring struct {
	activeKeyID string
	keys        map[string][32]byte
}

func (c ConversationAuditConfig) ParseKeyring() (ConversationAuditKeyring, error) {
	activeKeyID := strings.TrimSpace(c.ActiveKeyID)
	entries := strings.FieldsFunc(c.Keyring, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	if activeKeyID == "" && len(entries) == 0 {
		return ConversationAuditKeyring{}, nil
	}
	if !validConversationAuditKeyID(activeKeyID) {
		return ConversationAuditKeyring{}, fmt.Errorf("conversation_audit.active_key_id is invalid")
	}
	if len(entries) > maxConversationAuditKeys {
		return ConversationAuditKeyring{}, fmt.Errorf("conversation_audit.keyring exceeds %d keys", maxConversationAuditKeys)
	}

	parsed := ConversationAuditKeyring{
		activeKeyID: activeKeyID,
		keys:        make(map[string][32]byte, len(entries)),
	}
	for i, entry := range entries {
		keyID, material, ok := strings.Cut(strings.TrimSpace(entry), "=")
		keyID = strings.TrimSpace(keyID)
		material = strings.TrimSpace(material)
		if !ok || !validConversationAuditKeyID(keyID) || material == "" {
			return ConversationAuditKeyring{}, fmt.Errorf("conversation_audit.keyring entry %d is invalid", i+1)
		}
		if _, exists := parsed.keys[keyID]; exists {
			return ConversationAuditKeyring{}, fmt.Errorf("conversation_audit.keyring contains duplicate key id %q", keyID)
		}
		decoded, err := decodeConversationAuditKey(material)
		if err != nil {
			return ConversationAuditKeyring{}, fmt.Errorf("conversation_audit.keyring key %q must decode to 32 bytes", keyID)
		}
		var key [32]byte
		copy(key[:], decoded)
		parsed.keys[keyID] = key
	}
	if _, ok := parsed.keys[activeKeyID]; !ok {
		return ConversationAuditKeyring{}, fmt.Errorf("conversation_audit.active_key_id is not present in keyring")
	}
	return parsed, nil
}

func (k ConversationAuditKeyring) Configured() bool {
	_, ok := k.keys[k.activeKeyID]
	return k.activeKeyID != "" && ok
}

func (k ConversationAuditKeyring) ActiveKeyID() string { return k.activeKeyID }

func (k ConversationAuditKeyring) Key(keyID string) ([]byte, bool) {
	key, ok := k.keys[keyID]
	if !ok {
		return nil, false
	}
	result := make([]byte, len(key))
	copy(result, key[:])
	return result, true
}

func validConversationAuditKeyID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func decodeConversationAuditKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "hex:"):
		return decodeConversationAuditKeyHex(strings.TrimPrefix(value, "hex:"))
	case strings.HasPrefix(value, "base64:"):
		return decodeConversationAuditKeyBase64(strings.TrimPrefix(value, "base64:"))
	case len(value) == 64:
		if decoded, err := decodeConversationAuditKeyHex(value); err == nil {
			return decoded, nil
		}
	}
	return decodeConversationAuditKeyBase64(value)
}

func decodeConversationAuditKeyHex(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("invalid key material")
	}
	return decoded, nil
}

func decodeConversationAuditKeyBase64(value string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid key material")
}
