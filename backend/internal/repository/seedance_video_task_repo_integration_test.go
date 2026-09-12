//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSeedanceVideoTaskRepositoryPostgresCostSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		cost *service.SeedanceAccountCostSnapshot
	}{
		{"legacy_null", nil},
		{"video", &service.SeedanceAccountCostSnapshot{BillingMode: service.BillingModeVideo, UnitPrice: 0.03, RateMultiplier: 1}},
		{"free_video", &service.SeedanceAccountCostSnapshot{BillingMode: service.BillingModeVideo, RateMultiplier: 1}},
		{"per_task", &service.SeedanceAccountCostSnapshot{BillingMode: service.BillingModePerRequest, UnitPrice: 0.7, RateMultiplier: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo := NewSeedanceVideoTaskRepository(integrationDB)
			stateID := fmt.Sprintf("seedance-pg-%s-%d", tc.name, time.Now().UnixNano())
			t.Cleanup(func() {
				_, err := integrationDB.ExecContext(context.Background(),
					"DELETE FROM custom_seedance_video_tasks WHERE state_id = $1", stateID)
				require.NoError(t, err)
			})
			pending := &service.SeedanceVideoPendingBilling{
				StateID: stateID, HoldID: stateID, UserID: 7, APIKeyID: 8,
				Model: "seedance-2.0", Resolution: "480p", DurationSeconds: 4,
				TotalCostPerSecond: 0.1, ActualCostPerSecond: 0.1, RateMultiplier: 1,
			}
			// Creation precedes account selection, so a SQL NULL is required.
			require.NoError(t, repo.Create(ctx, pending))
			var emptyCost, emptyAccount bool
			require.NoError(t, integrationDB.QueryRowContext(ctx,
				"SELECT account_cost_snapshot IS NULL, account_id IS NULL FROM custom_seedance_video_tasks WHERE state_id = $1",
				stateID).Scan(&emptyCost, &emptyAccount))
			require.True(t, emptyCost)
			require.True(t, emptyAccount)

			require.NoError(t, repo.AssignAccount(ctx, stateID, 20, "fflink_v1", tc.cost))
			if tc.cost != nil {
				var jsonType string
				require.NoError(t, integrationDB.QueryRowContext(ctx,
					"SELECT jsonb_typeof(account_cost_snapshot) FROM custom_seedance_video_tasks WHERE state_id = $1",
					stateID).Scan(&jsonType))
				require.Equal(t, "object", jsonType)
			}
			require.NoError(t, repo.BindProviderTask(ctx, stateID, stateID, "queued", time.Now().Add(time.Hour)))
			loaded, err := repo.GetByProviderTask(ctx, stateID, 7, 8)
			require.NoError(t, err)
			require.Equal(t, tc.cost, loaded.AccountCost)
			require.Equal(t, int64(20), loaded.AccountID)
			require.Equal(t, "fflink_v1", loaded.ProviderID)

			// An accepted task must retain its selected account and price.
			require.ErrorIs(t, repo.AssignAccount(ctx, stateID, 21, "bblabu_v1", nil), service.ErrSeedanceVideoTaskNotFound)
			loaded, err = repo.GetByProviderTask(ctx, stateID, 7, 8)
			require.NoError(t, err)
			require.Equal(t, tc.cost, loaded.AccountCost)
			require.Equal(t, int64(20), loaded.AccountID)
		})
	}
}
