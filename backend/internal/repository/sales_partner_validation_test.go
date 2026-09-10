package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSalesPartnerAccountValidationIdentifiesUserField(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   int64
	}{
		{"account_is_not_an_existing_ordinary_user", 0},
		{"cannot_replace_partner_account", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := NewSalesRepository(client, db)
			mock.ExpectBegin()
			if tc.id == 0 {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users WHERE id=$1 AND deleted_at IS NULL AND role='user'")).
					WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			} else {
				at := time.Now()
				mock.ExpectQuery(regexp.QuoteMeta("SELECT " + salesPartnerColumns + " FROM sales_partners WHERE id=$1 FOR UPDATE")).
					WithArgs(tc.id).WillReturnRows(sqlmock.NewRows(strings.Split(salesPartnerColumns, ",")).
					AddRow(tc.id, 1, "QQ", "sales-qq", "qq.yusflow.com", 50, true, true, false, at, at))
			}
			mock.ExpectRollback()
			_, err = repo.SavePartner(context.Background(), tc.id, service.SalesPartnerInput{
				UserID: 2, Name: "QQ", Code: "sales-qq", Hostname: "qq.yusflow.com", CommissionRate: 50,
			}, 3)
			require.ErrorIs(t, err, service.ErrSalesInvalid)
			require.Equal(t, "user_id", infraerrors.FromError(err).Metadata["field"])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
