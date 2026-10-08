package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func expectBalanceConfigLock(mock sqlmock.Sqlmock, c *service.UpstreamBalanceConfig) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW()) ON CONFLICT (key) DO NOTHING")).WithArgs(upstreamBalanceConfigKey, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
	raw, _ := json.Marshal(c)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM settings WHERE key = $1 FOR UPDATE")).WithArgs(upstreamBalanceConfigKey).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
}

func TestUpstreamBalanceConfigRejectsStaleVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewUpstreamBalanceRepository(db)
	c := service.DefaultUpstreamBalanceConfig()
	c.Version = 2
	expectBalanceConfigLock(mock, c)
	mock.ExpectRollback()
	input := *c
	input.Version = 1
	_, err = repo.SaveConfig(context.Background(), &input)
	require.ErrorIs(t, err, service.ErrUpstreamBalanceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamBalanceConfigRemovesDeletedWalletSnapshotOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewUpstreamBalanceRepository(db)
	c := service.DefaultUpstreamBalanceConfig()
	c.Version = 2
	c.Wallets = []service.UpstreamBalanceWallet{{ID: "removed"}}
	expectBalanceConfigLock(mock, c)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE settings SET value=$1,updated_at=NOW() WHERE key=$2")).WithArgs(sqlmock.AnyArg(), upstreamBalanceConfigKey).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM settings WHERE key=$1")).WithArgs(upstreamBalanceSnapshotPrefix + "removed").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	input := *c
	input.Wallets = []service.UpstreamBalanceWallet{}
	saved, err := repo.SaveConfig(context.Background(), &input)
	require.NoError(t, err)
	require.Equal(t, int64(3), saved.Version)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamBalanceSnapshotRejectsInFlightConfigChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := service.DefaultUpstreamBalanceConfig()
	c.Version = 2
	expectBalanceConfigLock(mock, c)
	mock.ExpectRollback()
	err = NewUpstreamBalanceRepository(db).SaveSnapshot(context.Background(), 1, &service.Account{ID: 1}, &service.UpstreamBalanceSnapshot{Item: service.UpstreamBalanceItem{WalletID: "wallet"}})
	require.ErrorIs(t, err, service.ErrUpstreamBalanceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamBalanceSnapshotChecksIdentityWithoutLockingBusinessRows(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "rotated"}[changed], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			c := service.DefaultUpstreamBalanceConfig()
			c.Version = 2
			c.Wallets = []service.UpstreamBalanceWallet{{ID: "wallet"}}
			expectBalanceConfigLock(mock, c)
			// End-anchored SQL explicitly rejects any FOR SHARE/UPDATE on accounts.
			query := `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL
		AND type=$2 AND platform=$3 AND credentials=$4::jsonb
		AND proxy_id IS NOT DISTINCT FROM $5`
			rows := sqlmock.NewRows([]string{"id"})
			if !changed {
				rows.AddRow(1)
			}
			mock.ExpectQuery(regexp.QuoteMeta(query)+"$").WithArgs(int64(1), service.AccountTypeAPIKey, service.PlatformOpenAI, `{"api_key":"secret"}`, nil).WillReturnRows(rows)
			if changed {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec(regexp.QuoteMeta("INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW())")).WithArgs(upstreamBalanceSnapshotPrefix+"wallet", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			a := &service.Account{ID: 1, Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI, Credentials: map[string]any{"api_key": "secret"}}
			err = NewUpstreamBalanceRepository(db).SaveSnapshot(context.Background(), 2, a, &service.UpstreamBalanceSnapshot{Item: service.UpstreamBalanceItem{WalletID: "wallet"}})
			if changed {
				require.ErrorIs(t, err, service.ErrUpstreamBalanceConflict)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpstreamBalanceAbsentConfigIsDisabledWithoutWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM settings WHERE key = $1")).WithArgs(upstreamBalanceConfigKey).WillReturnRows(sqlmock.NewRows([]string{"value"}))
	c, err := NewUpstreamBalanceRepository(db).GetConfig(context.Background())
	require.NoError(t, err)
	require.False(t, c.Enabled)
	require.Empty(t, c.Wallets)
	require.Equal(t, 30, c.IntervalMinutes)
	require.NoError(t, mock.ExpectationsWereMet())
}
