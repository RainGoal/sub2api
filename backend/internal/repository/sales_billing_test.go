package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSalesBillingEventSharesDebitTransaction(t *testing.T) {
	for _, failEvent := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback_on_event_failure"}[failEvent], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			at, cost := time.Now().UTC(), 1.0
			cmd := &service.UsageBillingCommand{RequestID: "sales-request", APIKeyID: 2, UserID: 3,
				BalanceCost: 2, SalesCost: &cost, SalesConsumedAt: at, Model: "model"}
			cmd.Normalize()
			mock.ExpectBegin()
			mock.ExpectQuery("INSERT INTO usage_billing_dedup").
				WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			mock.ExpectQuery("SELECT request_fingerprint.*").
				WithArgs(cmd.RequestID, cmd.APIKeyID).WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery("UPDATE users").WithArgs(2.0, int64(3)).
				WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98))
			mock.ExpectQuery("SELECT p.id, r.id").WithArgs(int64(3), at).
				WillReturnRows(sqlmock.NewRows([]string{"id", "rule_id", "rate"}).AddRow(4, 5, "30.00000000"))
			event := mock.ExpectExec("INSERT INTO sales_commission_events").WithArgs(
				"sales-request", int64(2), int64(3), int64(4), int64(5),
				"2.00000000", "1.00000000", "1.00000000", "30.00000000", "0.30000000", "model", at)
			if failEvent {
				event.WillReturnError(errors.New("event unavailable"))
				mock.ExpectRollback()
			} else {
				event.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			result, err := (&usageBillingRepository{db: db}).Apply(context.Background(), cmd)
			if failEvent {
				require.ErrorContains(t, err, "append sales billing event")
			} else {
				require.NoError(t, err)
				require.True(t, result.Applied)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSalesBillingRetainsLossesAndSkipsUnownedUsers(t *testing.T) {
	for _, owned := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		at, cost := time.Now().UTC(), 3.0
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		query := mock.ExpectQuery("SELECT p.id, r.id").WithArgs(int64(3), at)
		if owned {
			query.WillReturnRows(sqlmock.NewRows([]string{"id", "rule_id", "rate"}).AddRow(4, 5, "30"))
			mock.ExpectExec("INSERT INTO sales_commission_events").WithArgs(
				"loss", int64(2), int64(3), int64(4), int64(5),
				"2.00000000", "3.00000000", "-1.00000000", "30.00000000", "-0.30000000", "", at).
				WillReturnResult(sqlmock.NewResult(1, 1))
		} else {
			query.WillReturnError(sql.ErrNoRows)
		}
		mock.ExpectRollback()
		err = appendSalesBillingEvent(context.Background(), tx, &service.UsageBillingCommand{
			RequestID: "loss", APIKeyID: 2, UserID: 3, BalanceCost: 2, SalesCost: &cost, SalesConsumedAt: at,
		})
		require.NoError(t, err)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func TestSalesBillingSkipsUnbilledAndSubscriptionUsage(t *testing.T) {
	// No database access is permitted for these non-commissionable events.
	for _, cmd := range []*service.UsageBillingCommand{
		nil, {}, {BillingType: service.BillingTypeSubscription, BalanceCost: 10},
	} {
		require.NoError(t, appendSalesBillingEvent(context.Background(), nil, cmd))
	}
}
