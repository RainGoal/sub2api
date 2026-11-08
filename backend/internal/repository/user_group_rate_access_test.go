package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGroupRateCandidatesIncludeExplicitAccessAndNullableOverrides(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rows := sqlmock.NewRows([]string{"user_id", "username", "email", "notes", "status", "rate_multiplier", "rpm_override", "has_explicit_access"}).
		AddRow(int64(2), "free", "free@example.com", "", "active", 0.0, nil, true).
		AddRow(int64(3), "standard", "standard@example.com", "", "active", 1.0, 20, false).
		AddRow(int64(4), "rpm", "rpm@example.com", "", "inactive", nil, 10, false)
	mock.ExpectQuery(`SELECT .*EXISTS .*FROM user_allowed_groups.*JOIN users u ON u.id = ugr.user_id AND u.deleted_at IS NULL`).WithArgs(int64(17)).WillReturnRows(rows)
	entries, err := NewUserGroupRateRepository(db).GetByGroupID(context.Background(), 17)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.True(t, entries[0].HasExplicitAccess)
	require.NotNil(t, entries[0].RateMultiplier)
	require.Zero(t, *entries[0].RateMultiplier)
	require.False(t, entries[1].HasExplicitAccess)
	require.Equal(t, 1.0, *entries[1].RateMultiplier)
	require.Equal(t, 20, *entries[1].RPMOverride)
	require.Nil(t, entries[2].RateMultiplier)
	require.Equal(t, 10, *entries[2].RPMOverride)
	require.NoError(t, mock.ExpectationsWereMet())
}
