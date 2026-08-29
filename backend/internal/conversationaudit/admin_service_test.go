package conversationaudit

import (
	"strings"
	"testing"
	"time"

	appconfig "github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAdminDeleteTokenBindsActorExpiryAndScope(t *testing.T) {
	service := NewAdminService(nil, nil, nil, &appconfig.Config{JWT: appconfig.JWTConfig{Secret: strings.Repeat("s", 32)}})
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	claims := deleteTokenClaims{
		Version: deleteTokenVersion, ActorID: 42, ExpiresAt: now.Add(time.Minute).Unix(),
		EligibilityCutoff: now.Add(-deleteEligibilityAge),
		Filter:            ListFilter{Start: now.Add(-24 * time.Hour), End: now},
		HighWater:         RecordCursor{CreatedAt: now.Add(-time.Hour), AuditID: uuid.New()},
		LowWater:          RecordCursor{CreatedAt: now.Add(-2 * time.Hour), AuditID: uuid.New()},
	}
	token, err := service.encodeDeleteToken(claims)
	require.NoError(t, err)
	decoded, err := service.decodeDeleteToken(token, 42)
	require.NoError(t, err)
	require.Equal(t, claims.HighWater.AuditID, decoded.HighWater.AuditID)

	_, err = service.decodeDeleteToken(token, 43)
	require.ErrorIs(t, err, ErrInvalidDeleteConfirmation)
	tampered := []byte(token)
	tampered[len(tampered)-1] ^= 1
	_, err = service.decodeDeleteToken(string(tampered), 42)
	require.ErrorIs(t, err, ErrInvalidDeleteConfirmation)
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	_, err = service.decodeDeleteToken(token, 42)
	require.ErrorIs(t, err, ErrInvalidDeleteConfirmation)
}

func TestAdminSigningDomainsAreSeparated(t *testing.T) {
	secret := strings.Repeat("s", 32)
	require.NotEqual(t, deriveAdminSigningKey(secret, "cursor"), deriveAdminSigningKey(secret, "delete"))
}
