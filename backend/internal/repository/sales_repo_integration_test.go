//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func salesTestFixture(t *testing.T) (context.Context, *dbent.Client, service.SalesRepository, *service.SalesPartner, *service.User) {
	t.Helper()
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	client := tx.Client()
	suffix := time.Now().UnixNano()
	owner := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("sales-owner-%d@example.com", suffix), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	customer := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("sales-customer-%d@example.com", suffix), PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
	repo := NewSalesRepository(client, integrationDB)
	p, err := repo.SavePartner(ctx, 0, service.SalesPartnerInput{UserID: owner.ID, Name: "Test Sales", Code: fmt.Sprintf("sales-%d", suffix), Hostname: fmt.Sprintf("sales-%d.example.com", suffix), CommissionRate: 50, PromotionEnabled: true, AccrualEnabled: true}, owner.ID)
	require.NoError(t, err)
	require.NoError(t, repo.BindCustomer(ctx, customer.ID, &service.SalesAttribution{PartnerID: p.ID, Code: p.Code, ExpiresAt: time.Now().Add(time.Hour).Unix()}))
	return ctx, client, repo, p, customer
}

func salesTestEvent(t *testing.T, ctx context.Context, client *dbent.Client, p *service.SalesPartner, customer *service.User, key string, rev, cost float64, at time.Time) {
	t.Helper()
	_, err := client.ExecContext(ctx, `INSERT INTO sales_commission_events(request_id,api_key_id,customer_user_id,partner_id,rule_id,revenue,cost,profit,commission_rate,commission,model,occurred_at)
SELECT $1,1,$2,$3,id,$4::numeric,$5::numeric,$4::numeric-$5::numeric,commission_rate,ROUND(($4::numeric-$5::numeric)*commission_rate/100,8),'test-model',$6 FROM sales_commission_rules WHERE partner_id=$3 ORDER BY id DESC LIMIT 1`, key, customer.ID, p.ID, rev, cost, at)
	require.NoError(t, err)
}

func TestSalesSettlementPreservesLossesAndPaymentIdempotency(t *testing.T) {
	ctx, client, repo, p, customer := salesTestFixture(t)
	salesTestEvent(t, ctx, client, p, customer, "loss-event", 1, 5, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	in := service.SalesSettlementInput{PartnerID: p.ID, Month: "2026-01", RequestKey: fmt.Sprintf("jan-%d", p.ID)}
	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	_, err := repo.CreateSettlement(ctx, in, cutoff, p.UserID)
	require.ErrorIs(t, err, service.ErrSalesPendingEvents)
	n, err := repo.ProcessEvents(ctx, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = repo.ProcessEvents(ctx, 100)
	require.NoError(t, err)
	require.Zero(t, n)
	jan, err := repo.CreateSettlement(ctx, in, cutoff, p.UserID)
	require.NoError(t, err)
	require.Equal(t, -2.0, jan.NetCommission)
	require.Zero(t, jan.PayoutAmount)
	require.Equal(t, -2.0, jan.CarryAmount)
	janRetry, err := repo.CreateSettlement(ctx, in, cutoff, p.UserID)
	require.NoError(t, err)
	require.Equal(t, jan.ID, janRetry.ID)
	salesTestEvent(t, ctx, client, p, customer, "gain-event", 20, 8, time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC))
	_, err = repo.ProcessEvents(ctx, 100)
	require.NoError(t, err)
	feb, err := repo.CreateSettlement(ctx, service.SalesSettlementInput{PartnerID: p.ID, Month: "2026-02", RequestKey: fmt.Sprintf("feb-%d", p.ID)}, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), p.UserID)
	require.NoError(t, err)
	require.Equal(t, 4.0, feb.NetCommission)
	require.Equal(t, 4.0, feb.PayoutAmount)
	_, err = repo.ConfirmSettlement(ctx, feb.ID, fmt.Sprintf("confirm-%d", feb.ID), p.UserID)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `UPDATE sales_partners SET payout_frozen=true WHERE id=$1`, p.ID)
	require.NoError(t, err)
	pay := service.SalesPaymentInput{RequestKey: fmt.Sprintf("payment-%d", feb.ID), PaymentReference: "bank-reference"}
	_, err = repo.PaySettlement(ctx, feb.ID, pay, p.UserID)
	require.ErrorIs(t, err, service.ErrSalesPayoutFrozen)
	_, err = client.ExecContext(ctx, `UPDATE sales_partners SET payout_frozen=false WHERE id=$1`, p.ID)
	require.NoError(t, err)
	paid, err := repo.PaySettlement(ctx, feb.ID, pay, p.UserID)
	require.NoError(t, err)
	require.Equal(t, "paid", paid.Status)
	paidAgain, err := repo.PaySettlement(ctx, feb.ID, pay, p.UserID)
	require.NoError(t, err)
	require.Equal(t, paid.PaidAt, paidAgain.PaidAt)
	pay.PaymentReference = "other-bank-reference"
	_, err = repo.PaySettlement(ctx, feb.ID, pay, p.UserID)
	require.ErrorIs(t, err, service.ErrSalesConflict)
	overview, err := repo.Overview(ctx, p.ID, service.SalesFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 1, overview.CustomerCount)
	require.Equal(t, 4.0, overview.Commission)
	require.Equal(t, 4.0, overview.PaidCommission)
}

func TestSalesAdjustmentIsIdempotentAndDoesNotRewriteHistory(t *testing.T) {
	ctx, client, repo, p, _ := salesTestFixture(t)
	in := service.SalesAdjustmentInput{PartnerID: p.ID, Commission: -1.23456789, Note: "Usage refund correction", RequestKey: fmt.Sprintf("adjust-%d", p.ID)}
	entry, err := repo.Adjust(ctx, in, p.UserID)
	require.NoError(t, err)
	retry, err := repo.Adjust(ctx, in, p.UserID)
	require.NoError(t, err)
	require.Equal(t, entry.ID, retry.ID)
	in.Commission = -2
	_, err = repo.Adjust(ctx, in, p.UserID)
	require.ErrorIs(t, err, service.ErrSalesConflict)
	// Must be last: the database rejects mutation and aborts this test transaction.
	_, err = client.ExecContext(ctx, `UPDATE sales_commission_ledger SET commission=0 WHERE id=$1`, entry.ID)
	require.Error(t, err)
}

func TestSalesBindingParticipatesInRegistrationTransaction(t *testing.T) {
	ctx, _, repo, p, customer := salesTestFixture(t)
	// Repeating exactly the same registration binding is harmless.
	a := &service.SalesAttribution{PartnerID: p.ID, Code: p.Code, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, repo.BindCustomer(ctx, customer.ID, a))
	require.ErrorIs(t, repo.BindCustomer(ctx, p.UserID, a), service.ErrSalesInvalid)
	items, total, err := repo.ListCustomers(ctx, service.SalesFilter{PartnerID: p.ID, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
}
