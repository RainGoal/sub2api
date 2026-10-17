package repository

import "github.com/DATA-DOG/go-sqlmock"

// Credential replacement also checks for video dependencies. These fixtures
// represent non-video accounts, so the seedance-only lock finds no row.
func expectNoVideoAccountCredentialLock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`(?s)SELECT id, credentials, type FROM accounts.*platform = 'seedance'.*ORDER BY id FOR UPDATE`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "type"}))
}
