package config

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationAuditKeyringSupportsRotation(t *testing.T) {
	oldKey := strings.Repeat("11", 32)
	newKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
	keyring, err := (ConversationAuditConfig{
		ActiveKeyID: "2026-08",
		Keyring:     "legacy=hex:" + oldKey + ",2026-08=base64:" + newKey,
	}).ParseKeyring()
	require.NoError(t, err)
	require.True(t, keyring.Configured())
	require.Equal(t, "2026-08", keyring.ActiveKeyID())

	active, ok := keyring.Key("2026-08")
	require.True(t, ok)
	require.Equal(t, []byte(strings.Repeat("n", 32)), active)
	active[0] = 'x'
	again, ok := keyring.Key("2026-08")
	require.True(t, ok)
	require.Equal(t, byte('n'), again[0], "callers must not mutate keyring storage")

	legacy, ok := keyring.Key("legacy")
	require.True(t, ok)
	require.Len(t, legacy, 32)
}

func TestConversationAuditKeyringAllowsUnconfiguredDisabledDeployment(t *testing.T) {
	keyring, err := (ConversationAuditConfig{}).ParseKeyring()
	require.NoError(t, err)
	require.False(t, keyring.Configured())
}

func TestConversationAuditKeyringRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		cfg  ConversationAuditConfig
	}{
		{name: "missing active", cfg: ConversationAuditConfig{Keyring: "v1=" + strings.Repeat("11", 32)}},
		{name: "active missing", cfg: ConversationAuditConfig{ActiveKeyID: "v2", Keyring: "v1=" + strings.Repeat("11", 32)}},
		{name: "invalid id", cfg: ConversationAuditConfig{ActiveKeyID: "bad id", Keyring: "bad id=" + strings.Repeat("11", 32)}},
		{name: "duplicate", cfg: ConversationAuditConfig{ActiveKeyID: "v1", Keyring: "v1=" + strings.Repeat("11", 32) + ",v1=" + strings.Repeat("22", 32)}},
		{name: "short key", cfg: ConversationAuditConfig{ActiveKeyID: "v1", Keyring: "v1=0011"}},
		{name: "malformed entry", cfg: ConversationAuditConfig{ActiveKeyID: "v1", Keyring: "v1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.cfg.ParseKeyring()
			require.Error(t, err)
		})
	}
}

func TestLoadConversationAuditKeyringFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("CONVERSATION_AUDIT_ACTIVE_KEY_ID", "v1")
	t.Setenv("CONVERSATION_AUDIT_KEYRING", "v1="+strings.Repeat("42", 32))

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "v1", cfg.ConversationAudit.ActiveKeyID)
	require.NotContains(t, cfg.ConversationAudit.Keyring, " ")
	keyring, err := cfg.ConversationAudit.ParseKeyring()
	require.NoError(t, err)
	require.True(t, keyring.Configured())
}
