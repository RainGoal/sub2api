package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Implemented on the account repository as well, so all administrator account
// mutations can protect task credentials without adding a global dependency.
func (r *accountRepository) HasUnfinishedForAccount(ctx context.Context, accountID int64) (bool, error) {
	if r == nil || r.sql == nil || accountID <= 0 {
		return false, errors.New("video task account dependency lookup is invalid")
	}
	return querySeedanceVideoTaskAccountDependencies(ctx, r.sql, accountID)
}

// The share lock and dependency write share one transaction. A concurrent
// credential update/delete must obtain the conflicting update lock first.
func lockVideoTaskExecutionAccount(ctx context.Context, tx *sql.Tx, accountID int64, execution *service.VideoTasksExecutionSnapshot, expectedAPIKey string) error {
	var credentialsJSON []byte
	err := tx.QueryRowContext(ctx, `SELECT credentials FROM accounts
WHERE id = $1 AND platform = 'seedance' AND type = 'apikey' AND deleted_at IS NULL
FOR SHARE`, accountID).Scan(&credentialsJSON)
	if err != nil {
		return err
	}
	var credentials map[string]any
	if err := json.Unmarshal(credentialsJSON, &credentials); err != nil {
		return err
	}
	account := &service.Account{Platform: service.PlatformSeedance, Type: service.AccountTypeAPIKey, Credentials: credentials}
	if expectedAPIKey == "" || account.GetSeedanceAPIKey() != expectedAPIKey || string(account.GetVideoProviderID()) != execution.Protocol ||
		strings.TrimRight(account.GetVideoProviderBaseURL(execution.Protocol), "/") != strings.TrimRight(execution.BaseURL, "/") {
		return errors.New("video account execution identity changed before assignment")
	}
	return nil
}

// Call only inside the same transaction that will mutate the account. Locks
// precede dependency reads, so assignment cannot slip between check and write.
func guardVideoAccountMutation(ctx context.Context, exec sqlExecutor, ids []int64, desired *service.Account, delta map[string]any, deleting bool) error {
	if desired == nil && !deleting {
		if _, changesKey := delta["api_key"]; !changesKey {
			return nil
		}
	}
	rows, err := exec.QueryContext(ctx, `SELECT id, credentials, type FROM accounts
WHERE id = ANY($1) AND platform = 'seedance' AND deleted_at IS NULL ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	var changedIDs []int64
	for rows.Next() {
		var id int64
		var encoded []byte
		var accountType string
		if err := rows.Scan(&id, &encoded, &accountType); err != nil {
			_ = rows.Close()
			return err
		}
		var credentials map[string]any
		if err := json.Unmarshal(encoded, &credentials); err != nil {
			_ = rows.Close()
			return err
		}
		oldKey, _ := credentials["api_key"].(string)
		oldKey = strings.TrimSpace(oldKey)
		newKey := oldKey
		if value, provided := delta["api_key"]; provided {
			newKey, _ = value.(string)
			newKey = strings.TrimSpace(newKey)
		}
		changed := deleting || newKey != oldKey
		if desired != nil {
			changed = changed || desired.Type != accountType || desired.Platform != service.PlatformSeedance || desired.GetSeedanceAPIKey() != oldKey
		}
		if changed {
			changedIDs = append(changedIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range changedIDs {
		protected, err := querySeedanceVideoTaskAccountDependencies(ctx, exec, id)
		if err != nil {
			return err
		}
		if protected {
			return service.ErrVideoTaskCredentialsProtected
		}
	}
	return nil
}
