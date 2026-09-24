package repository

import (
	"context"
	"encoding/json"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLockAndMergeAccountProbeExtraPreservesCostPricingWithOpenCodeUsage(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	account := openCodeGoUsageRepositoryAccount()
	account.Extra = map[string]any{
		"custom": "edited",
		service.OpenCodeGoUsageAutoRefreshExtraKey: false,
		service.OpenCodeGoUsageSnapshotExtraKey:    map[string]any{"status": "forged"},
	}
	const pricing = `[{"platform":"openai","models":["vendor-model"],"billing_mode":"per_request","per_request_price":0.04}]`
	credentials, err := json.Marshal(account.Credentials)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT.*extra -> 'model_cost_pricing'.*FOR NO KEY UPDATE`).
		WithArgs(account.ID, account.Platform, account.Type, string(credentials), nil).
		WillReturnRows(sqlmock.NewRows(openCodeGoMergeMockColumns()).
			AddRow(true, false, true, nil, nil, nil, nil, nil, nil, true, "true", openCodeGoSnapshotJSON(), pricing))

	got, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "edited", got["custom"])
	require.Equal(t, true, got[service.OpenCodeGoUsageAutoRefreshExtraKey])
	snapshot, err := json.Marshal(got[service.OpenCodeGoUsageSnapshotExtraKey])
	require.NoError(t, err)
	require.JSONEq(t, openCodeGoSnapshotJSON(), string(snapshot))
	cost, err := json.Marshal(got[service.AccountModelCostPricingExtraKey])
	require.NoError(t, err)
	require.JSONEq(t, pricing, string(cost))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockAndMergeAccountProbeExtraPreservesModelCostPricing(t *testing.T) {
	const current = `[{"platform":"openai","models":["vendor-model"],"billing_mode":"per_request","per_request_price":0.04}]`
	for _, tt := range []struct {
		name     string
		input    map[string]any
		database any
		want     string
	}{
		{"omitted key uses current database price", map[string]any{"quota_limit": 100}, []byte(current), current},
		{"nil extra uses current database price", nil, []byte(current), current},
		{"empty array clears current price", map[string]any{service.AccountModelCostPricingExtraKey: []any{}}, []byte(current), `[]`},
		{"explicit update replaces current price", map[string]any{service.AccountModelCostPricingExtraKey: []any{map[string]any{"per_request_price": 0.05}}}, []byte(current), `[{"per_request_price":0.05}]`},
		{"legacy account stays unconfigured", nil, nil, ``},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectQuery(`(?s)SELECT.*extra -> 'model_cost_pricing'.*FOR NO KEY UPDATE`).
				WithArgs(int64(27), service.PlatformOpenAI, service.AccountTypeAPIKey, `{}`, nil).
				WillReturnRows(sqlmock.NewRows(openCodeGoMergeMockColumns()).
					AddRow(true, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, tt.database))
			account := &service.Account{ID: 27, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: tt.input}
			got, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			if tt.want == "" {
				require.NotContains(t, got, service.AccountModelCostPricingExtraKey)
			} else {
				data, err := json.Marshal(got[service.AccountModelCostPricingExtraKey])
				require.NoError(t, err)
				require.JSONEq(t, tt.want, string(data))
			}
			if limit, ok := tt.input["quota_limit"]; ok {
				require.Equal(t, limit, got["quota_limit"])
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
