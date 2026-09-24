package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Keep group reads in memory while exercising the real repository write/CAS.
type openCodeGoResolvedGroupTestRepo struct {
	*accountRepository
	accounts []*service.Account
}

func (r *openCodeGoResolvedGroupTestRepo) ListOpenCodeGoUsageGroupAccounts(context.Context, []*service.Account) ([]service.Account, error) {
	result := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		result = append(result, *account)
	}
	return result, nil
}

func TestOpenCodeGoResolvedGroupRemainsWritableAfterAddingSibling(t *testing.T) {
	for _, operation := range []string{"toggle", "snapshot"} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/enabled=%t", operation, enabled), func(t *testing.T) {
				client, mock := newOllamaCloudUsageRepositoryTestClient(t)
				source := openCodeGoUsageRepositoryAccount()
				source.Extra[service.OpenCodeGoUsageAutoRefreshExtraKey] = enabled
				source.UpdatedAt = time.Now().Add(-time.Hour)
				sibling := *source
				sibling.ID++
				sibling.UpdatedAt = time.Now()
				sibling.Extra = map[string]any{}
				repo := &openCodeGoResolvedGroupTestRepo{
					accountRepository: newAccountRepositoryWithSQL(client, nil, nil),
					accounts:          []*service.Account{source, &sibling},
				}
				svc := service.NewOpenCodeGoUsageService(repo, nil, nil)
				t.Cleanup(svc.Stop)
				target := sibling
				target.Extra = map[string]any{}
				require.NoError(t, svc.ResolveOpenCodeGoUsageAccounts(context.Background(), []*service.Account{&target}))
				state := service.OpenCodeGoUsageStateFromAccount(&target)
				require.Equal(t, enabled, state.AutoRefreshEnabled)
				require.NotNil(t, state.Snapshot)

				snapshotJSON, err := json.Marshal(source.Extra[service.OpenCodeGoUsageSnapshotExtraKey])
				require.NoError(t, err)
				credentials, err := json.Marshal(source.Credentials)
				require.NoError(t, err)
				nextSnapshot := &service.OpenCodeGoUsageSnapshot{
					Status: service.OpenCodeGoUsageStatusOK, LastAttemptAt: time.Now().UTC(),
				}
				payload := map[string]any{service.OpenCodeGoUsageAutoRefreshExtraKey: !enabled}
				if operation == "snapshot" {
					payload = map[string]any{service.OpenCodeGoUsageSnapshotExtraKey: nextSnapshot}
				}
				payloadJSON, err := json.Marshal(payload)
				require.NoError(t, err)
				mock.ExpectBegin()
				mock.ExpectQuery(`(?s)SELECT.*FOR NO KEY UPDATE`).
					WithArgs("key", target.ID, target.Platform, target.Type, string(credentials), nil).
					WillReturnRows(sqlmock.NewRows([]string{"id", "anchor_matches", "auto_refresh", "snapshot"}).
						AddRow(source.ID, false, fmt.Sprint(enabled), string(snapshotJSON)).
						AddRow(sibling.ID, true, "null", "null"))
				mock.ExpectExec(`(?s)UPDATE accounts`).
					WithArgs(string(payloadJSON), "key", sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 2))
				mock.ExpectCommit()
				if operation == "snapshot" {
					err = repo.UpdateOpenCodeGoUsageSnapshot(context.Background(), &target, nextSnapshot)
				} else {
					err = repo.SetOpenCodeGoUsageAutoRefresh(context.Background(), &target, !enabled)
				}
				require.NoError(t, err, "the resolved state must match a stored group member for CAS")
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
