//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAdminGroupAccessGrantsAtomic(t *testing.T) {
	for _, failure := range []string{"", "grant", "scheduler"} {
		t.Run("failure="+failure, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			suffix := time.Now().UnixNano()
			group := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("access-%d", suffix), RateMultiplier: 1})
			other := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("access-other-%d", suffix), RateMultiplier: 1, IsExclusive: true})
			selected := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("access-selected-%d@example.com", suffix), AllowedGroups: []int64{other.ID}})
			existing := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("access-existing-%d@example.com", suffix), AllowedGroups: []int64{group.ID}})
			unselected := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("access-unselected-%d@example.com", suffix)})
			keyValue := fmt.Sprintf("sk-access-%d", suffix)
			key, err := client.APIKey.Create().SetUserID(selected.ID).SetGroupID(group.ID).SetName("access").SetKey(keyValue).Save(ctx)
			require.NoError(t, err)
			sum := sha256.Sum256([]byte(keyValue))
			cacheKey := hex.EncodeToString(sum[:])
			functionName := fmt.Sprintf("fail_access_%d", suffix)
			triggerName := fmt.Sprintf("fail_access_trigger_%d", suffix)
			table := "user_allowed_groups"
			if failure == "scheduler" {
				table = "scheduler_outbox"
			}
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", triggerName, table))
				_, _ = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_group_rate_multipliers WHERE group_id IN ($1,$2)", group.ID, other.ID)
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_allowed_groups WHERE group_id IN ($1,$2)", group.ID, other.ID)
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id=$1", key.ID)
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id IN ($1,$2,$3)", selected.ID, existing.ID, unselected.ID)
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1,$2)", group.ID, other.ID)
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE group_id IN ($1,$2)", group.ID, other.ID)
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=$1", cacheKey)
			})
			_, err = integrationDB.ExecContext(ctx, `INSERT INTO user_group_rate_multipliers (user_id,group_id,rate_multiplier,rpm_override)
				VALUES ($1,$2,0,11),($3,$2,1.2,NULL)`, selected.ID, group.ID, unselected.ID)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=$1", cacheKey)
			require.NoError(t, err)
			if failure != "" {
				condition := fmt.Sprintf("NEW.group_id = %d", group.ID)
				if failure == "grant" {
					condition += fmt.Sprintf(" AND NEW.user_id = %d", selected.ID)
				}
				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN IF %s THEN RAISE EXCEPTION 'forced access failure'; END IF; RETURN NEW; END; $$`, functionName, condition))
				require.NoError(t, err)
				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, table, functionName))
				require.NoError(t, err)
			}
			users := NewUserRepository(client, integrationDB)
			groups := newGroupRepositoryWithSQL(client, integrationDB)
			svc := service.NewAdminService(nil, users, groups, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			exclusive := true
			_, err = svc.UpdateGroup(ctx, group.ID, &service.UpdateGroupInput{IsExclusive: &exclusive, GrantUserIDs: []int64{existing.ID, selected.ID, selected.ID}})
			if failure == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "forced access failure")
			}
			stored, err := groups.GetByID(ctx, group.ID)
			require.NoError(t, err)
			require.Equal(t, failure == "", stored.IsExclusive)
			selectedAfter, err := users.GetByID(ctx, selected.ID)
			require.NoError(t, err)
			require.Contains(t, selectedAfter.AllowedGroups, other.ID)
			if failure == "" {
				require.Contains(t, selectedAfter.AllowedGroups, group.ID)
			} else {
				require.NotContains(t, selectedAfter.AllowedGroups, group.ID)
			}
			existingAfter, err := users.GetByID(ctx, existing.ID)
			require.NoError(t, err)
			require.Contains(t, existingAfter.AllowedGroups, group.ID)
			unselectedAfter, err := users.GetByID(ctx, unselected.ID)
			require.NoError(t, err)
			require.NotContains(t, unselectedAfter.AllowedGroups, group.ID)
			var rate float64
			var rpm int
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT rate_multiplier,rpm_override FROM user_group_rate_multipliers WHERE user_id=$1 AND group_id=$2", selected.ID, group.ID).Scan(&rate, &rpm))
			require.Zero(t, rate)
			require.Equal(t, 11, rpm)
			keyAfter, err := client.APIKey.Get(ctx, key.ID)
			require.NoError(t, err)
			require.Equal(t, group.ID, *keyAfter.GroupID)
			var schedulerEvents, authEvents int
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE group_id=$1", group.ID).Scan(&schedulerEvents))
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key=$1", cacheKey).Scan(&authEvents))
			if failure != "" {
				require.Zero(t, schedulerEvents)
				require.Zero(t, authEvents)
				return
			}
			require.Positive(t, schedulerEvents)
			require.Positive(t, authEvents)
			// Repeating a request against an already-exclusive group is additive and idempotent.
			_, err = svc.UpdateGroup(ctx, group.ID, &service.UpdateGroupInput{GrantUserIDs: []int64{selected.ID}})
			require.NoError(t, err)
			var count int
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_allowed_groups WHERE user_id=$1 AND group_id=$2", selected.ID, group.ID).Scan(&count))
			require.Equal(t, 1, count)
			entries, err := NewUserGroupRateRepository(integrationDB).GetByGroupID(ctx, group.ID)
			require.NoError(t, err)
			require.Len(t, entries, 2)
			for _, entry := range entries {
				require.Equal(t, entry.UserID == selected.ID, entry.HasExplicitAccess)
			}
		})
	}
}
