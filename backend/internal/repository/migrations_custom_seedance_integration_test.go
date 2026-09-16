//go:build integration

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestMigrationsRunner_CustomSeedanceQuotaUpgrade(t *testing.T) {
	for _, alreadyApplied := range []bool{false, true} {
		name := "237_with_seedance_quota"
		if alreadyApplied {
			name = "original_238_already_applied"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			db := newCustomSeedanceMigrationTestDB(t, ctx)
			fsys := fstest.MapFS{
				"000_custom_seedance_test_fixture.sql": &fstest.MapFile{Data: []byte(`
CREATE TABLE users (id BIGINT PRIMARY KEY);
INSERT INTO users (id) VALUES (1), (2), (3);
CREATE TABLE composite_model_routes (target_platform TEXT NOT NULL);
CREATE TABLE channel_monitors (provider TEXT NOT NULL);
CREATE TABLE channel_monitor_request_templates (provider TEXT NOT NULL);
CREATE TABLE custom_seedance_video_tasks (id BIGINT PRIMARY KEY);
`)},
			}
			addCustomSeedanceTestMigrations(t, fsys,
				"142_user_platform_quotas.sql", "237_add_minimax_platform.sql",
				"238_seedance_account_cost_snapshot.sql")
			require.NoError(t, applyMigrationsFS(ctx, db, fsys))
			original, err := migrations.FS.ReadFile(openCodePlatformMigration)
			require.NoError(t, err)
			platform := "seedance"
			if alreadyApplied {
				// Reproduce the previous binary: execute the immutable SQL verbatim,
				// and record its checksum atomically before running the repaired runner.
				tx, err := db.BeginTx(ctx, nil)
				require.NoError(t, err)
				defer func() { _ = tx.Rollback() }()
				_, err = tx.ExecContext(ctx, string(original))
				require.NoError(t, err)
				_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)",
					openCodePlatformMigration, migrationChecksum(string(original)))
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				_, err = db.ExecContext(ctx, "INSERT INTO user_platform_quotas (user_id, platform) VALUES (3, 'seedance')")
				requireCustomSeedanceCheckViolation(t, err)
				platform = "opencode_go"
			}
			_, err = db.ExecContext(ctx, `
INSERT INTO user_platform_quotas (
    user_id, platform, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
    daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
    daily_window_start, weekly_window_start, monthly_window_start
) VALUES (1, $1, 10, 50, 100, 1.25, 2.5, 3.75,
          '2026-09-16 00:00:00+00', '2026-09-14 00:00:00+00', '2026-09-01 00:00:00+00')`, platform)
			require.NoError(t, err)
			var before string
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT row_to_json(q)::text FROM user_platform_quotas q WHERE user_id = 1").Scan(&before))
			addCustomSeedanceTestMigrations(t, fsys, openCodePlatformMigration, "238_purge_unlimited_user_platform_quotas.sql")
			// In the pending path this must succeed before 239 is even present.
			// Merely adding a later repair cannot pass this upgrade assertion.
			require.NoError(t, applyMigrationsFS(ctx, db, fsys))
			addCustomSeedanceTestMigrations(t, fsys, "239_custom_seedance_quota_platform.sql")
			for range 2 {
				require.NoError(t, applyMigrationsFS(ctx, db, fsys))
			}
			var after, checksum string
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT row_to_json(q)::text FROM user_platform_quotas q WHERE user_id = 1").Scan(&after))
			require.JSONEq(t, before, after, "quota limits, usage and window timestamps must survive upgrade")
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT checksum FROM schema_migrations WHERE filename = $1", openCodePlatformMigration).Scan(&checksum))
			require.Equal(t, migrationChecksum(string(original)), checksum, "never replace historical migration checksums")
			var applied int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied))
			require.Equal(t, len(fsys), applied, "all distinct 238 migrations must be tracked independently")
			assertCustomSeedanceMigrationPlatforms(t, ctx, db)
		})
	}
}

func addCustomSeedanceTestMigrations(t *testing.T, fsys fstest.MapFS, names ...string) {
	t.Helper()
	for _, name := range names {
		data, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		fsys[name] = &fstest.MapFile{Data: data}
	}
}

func newCustomSeedanceMigrationTestDB(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	// Use a separate PostgreSQL instance so historical constraints and migration
	// records cannot affect the shared integration database or other tests.
	container, err := tcpostgres.Run(ctx, selectDockerImage(ctx, postgresImageTag),
		tcpostgres.WithDatabase("seedance_migration_test"), tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))
	return db
}

func assertCustomSeedanceMigrationPlatforms(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	platforms := []string{"anthropic", "openai", "gemini", "antigravity", "grok", "seedance", "kimi", "zhipu", "deepseek", "minimax", "opencode_go"}
	for _, platform := range platforms {
		_, err := db.ExecContext(ctx,
			"INSERT INTO user_platform_quotas (user_id, platform, daily_limit_usd) VALUES (2, $1, 10)", platform)
		require.NoError(t, err, "quota platform %s", platform)
	}
	_, err := db.ExecContext(ctx,
		"INSERT INTO user_platform_quotas (user_id, platform) VALUES (2, 'invalid_platform')")
	requireCustomSeedanceCheckViolation(t, err)
	for _, table := range []string{"composite_model_routes", "channel_monitors", "channel_monitor_request_templates"} {
		column := "provider"
		if strings.HasPrefix(table, "composite_") {
			column = "target_platform"
		}
		query := "INSERT INTO " + pq.QuoteIdentifier(table) + " (" + pq.QuoteIdentifier(column) + ") VALUES ($1)"
		for _, platform := range platforms {
			_, err = db.ExecContext(ctx, query, platform)
			if platform == "seedance" {
				requireCustomSeedanceCheckViolation(t, err)
			} else {
				require.NoError(t, err, "%s platform %s", table, platform)
			}
		}
		_, err = db.ExecContext(ctx, query, "invalid_platform")
		requireCustomSeedanceCheckViolation(t, err)
	}
}

func requireCustomSeedanceCheckViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr *pq.Error
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", string(pgErr.Code))
}
