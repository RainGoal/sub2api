package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestCustomSeedanceMigrationExecutionSQL(t *testing.T) {
	data, err := migrations.FS.ReadFile(openCodePlatformMigration)
	require.NoError(t, err)
	lfContent := strings.ReplaceAll(strings.TrimSpace(string(data)), "\r\n", "\n")
	for _, lineEnding := range []string{"\n", "\r\n"} {
		content := strings.ReplaceAll(lfContent, "\n", lineEnding)
		checksum := migrationChecksum(content)
		require.Contains(t, []string{openCodePlatformMigrationChecksumLF, openCodePlatformMigrationChecksumCRLF}, checksum,
			"the immutable upstream migration must still match the guarded content")
		want := strings.Replace(content, "'kimi'", "'seedance', 'kimi'", 1)
		require.NotEqual(t, content, want)
		require.Equal(t, want, customSeedanceMigrationExecutionSQL(openCodePlatformMigration, checksum, content))
		require.Equal(t, 1, strings.Count(want, "'seedance'"), "do not extend route or monitor providers")
		require.Equal(t, content, customSeedanceMigrationExecutionSQL("other.sql", checksum, content))
		require.Equal(t, content, customSeedanceMigrationExecutionSQL(openCodePlatformMigration, "unknown", content))
		changed := content + lineEnding + "SELECT 1;"
		require.Equal(t, changed, customSeedanceMigrationExecutionSQL(openCodePlatformMigration, migrationChecksum(changed), changed))
	}
}

func TestApplyMigrationsFS_CustomSeedanceKeepsOriginalChecksum(t *testing.T) {
	data, err := migrations.FS.ReadFile(openCodePlatformMigration)
	require.NoError(t, err)
	content := strings.TrimSpace(string(data))
	checksum := migrationChecksum(content)
	fsys := fstest.MapFS{openCodePlatformMigration: &fstest.MapFile{Data: data}}
	for _, state := range []string{"pending", "applied", "tampered"} {
		t.Run(state, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			prepareMigrationsBootstrapExpectations(mock)
			query := mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
				WithArgs(openCodePlatformMigration)
			switch state {
			case "pending":
				query.WillReturnRows(sqlmock.NewRows([]string{"checksum"}))
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta(strings.Replace(content, "'kimi'", "'seedance', 'kimi'", 1))).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("INSERT INTO schema_migrations").WithArgs(openCodePlatformMigration, checksum).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			case "applied":
				query.WillReturnRows(sqlmock.NewRows([]string{"checksum"}).AddRow(checksum))
			case "tampered":
				query.WillReturnRows(sqlmock.NewRows([]string{"checksum"}).AddRow("unknown"))
			}
			mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").WithArgs(migrationsAdvisoryLockID).
				WillReturnResult(sqlmock.NewResult(0, 1))
			err = applyMigrationsFS(context.Background(), db, fsys)
			if state == "tampered" {
				require.ErrorContains(t, err, "checksum mismatch")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
