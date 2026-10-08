package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const upstreamBalanceConfigKey = "custom_upstream_balance_config"
const upstreamBalanceSnapshotPrefix = "custom_upstream_balance_snapshot:"

type upstreamBalanceRepository struct{ db *sql.DB }

func NewUpstreamBalanceRepository(db *sql.DB) service.UpstreamBalanceRepository {
	return &upstreamBalanceRepository{db: db}
}

func NewUpstreamBalanceHTTPUpstream(cfg *config.Config) service.UpstreamBalanceHTTPUpstream {
	return service.UpstreamBalanceHTTPUpstream{HTTPUpstream: NewHTTPUpstream(cfg)}
}

func (r *upstreamBalanceRepository) GetConfig(ctx context.Context) (*service.UpstreamBalanceConfig, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = $1", upstreamBalanceConfigKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return service.DefaultUpstreamBalanceConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	result := service.DefaultUpstreamBalanceConfig()
	if err := json.Unmarshal([]byte(raw), result); err != nil {
		return nil, err
	}
	return result, nil
}

// The private config row serializes config edits and snapshot writes, including
// initial creation. No account, scheduler outbox, billing or global setting is written.
func lockUpstreamBalanceConfig(ctx context.Context, tx *sql.Tx) (*service.UpstreamBalanceConfig, error) {
	initial, _ := json.Marshal(service.DefaultUpstreamBalanceConfig())
	if _, err := tx.ExecContext(ctx, "INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW()) ON CONFLICT (key) DO NOTHING", upstreamBalanceConfigKey, string(initial)); err != nil {
		return nil, err
	}
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = $1 FOR UPDATE", upstreamBalanceConfigKey).Scan(&raw); err != nil {
		return nil, err
	}
	result := service.DefaultUpstreamBalanceConfig()
	if err := json.Unmarshal([]byte(raw), result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *upstreamBalanceRepository) SaveConfig(ctx context.Context, input *service.UpstreamBalanceConfig) (*service.UpstreamBalanceConfig, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := lockUpstreamBalanceConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	if current.Version != input.Version {
		return nil, service.ErrUpstreamBalanceConflict
	}
	result := *input
	result.Version++
	raw, err := json.Marshal(&result)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=$1,updated_at=NOW() WHERE key=$2", string(raw), upstreamBalanceConfigKey); err != nil {
		return nil, err
	}
	retained := map[string]bool{}
	for _, w := range result.Wallets {
		retained[w.ID] = true
	}
	for _, w := range current.Wallets {
		if !retained[w.ID] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key=$1", upstreamBalanceSnapshotPrefix+w.ID); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *upstreamBalanceRepository) GetSnapshots(ctx context.Context) (map[string]*service.UpstreamBalanceSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT key,value FROM settings WHERE starts_with(key,$1)", upstreamBalanceSnapshotPrefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := map[string]*service.UpstreamBalanceSnapshot{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var snapshot service.UpstreamBalanceSnapshot
		if json.Unmarshal([]byte(raw), &snapshot) == nil {
			result[strings.TrimPrefix(key, upstreamBalanceSnapshotPrefix)] = &snapshot
		}
	}
	return result, rows.Err()
}

func (r *upstreamBalanceRepository) SaveSnapshot(ctx context.Context, version int64, expected *service.Account, snapshot *service.UpstreamBalanceSnapshot) error {
	if expected == nil || snapshot == nil {
		return service.ErrUpstreamBalanceInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := lockUpstreamBalanceConfig(ctx, tx)
	if err != nil {
		return err
	}
	if current.Version != version {
		return service.ErrUpstreamBalanceConflict
	}
	found := false
	for _, w := range current.Wallets {
		if w.ID == snapshot.Item.WalletID {
			found = true
			break
		}
	}
	if !found {
		return service.ErrUpstreamBalanceConflict
	}
	credentials, err := json.Marshal(expected.Credentials)
	if err != nil {
		return err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL
		AND type=$2 AND platform=$3 AND credentials=$4::jsonb
		AND proxy_id IS NOT DISTINCT FROM $5`, expected.ID, expected.Type, expected.Platform, string(credentials), expected.ProxyID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrUpstreamBalanceConflict
	}
	if err != nil {
		return err
	}
	if expected.ProxyID != nil {
		if expected.Proxy == nil {
			return service.ErrUpstreamBalanceConflict
		}
		p := expected.Proxy
		err := tx.QueryRowContext(ctx, `SELECT id FROM proxies WHERE id=$1 AND deleted_at IS NULL
			AND protocol=$2 AND host=$3 AND port=$4 AND COALESCE(username,'')=$5
			AND COALESCE(password,'')=$6`, *expected.ProxyID, p.Protocol, p.Host, p.Port, p.Username, p.Password).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrUpstreamBalanceConflict
		}
		if err != nil {
			return err
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, upstreamBalanceSnapshotPrefix+snapshot.Item.WalletID, string(raw))
	if err != nil {
		return err
	}
	return tx.Commit()
}
