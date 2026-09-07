package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Opt-in isolated SQL verification, usable with a portable local PostgreSQL or
// PGlite. The ordinary test suite never needs a server. Each test creates and
// removes only its own schema; external/database-production hosts are rejected.
func salesRealDatabase(t *testing.T) (*sql.DB, *dbent.Client, service.SalesRepository, bool) {
	t.Helper()
	dsn := os.Getenv("SUB2API_SALES_QA_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SUB2API_SALES_QA_DATABASE_URL to an isolated local PostgreSQL/PGlite server")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, u.Hostname(), "QA must use a local isolated server")
	bootstrap, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	var version string
	require.NoError(t, bootstrap.QueryRow(`SELECT version()`).Scan(&version))
	isPGlite := strings.Contains(version, "PGlite")
	schema := fmt.Sprintf("salesqa_%d", time.Now().UnixNano())
	_, err = bootstrap.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	require.NoError(t, bootstrap.Close())
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	if isPGlite {
		db.SetMaxOpenConns(1)
		_, err = db.Exec(`SET search_path TO ` + schema)
		require.NoError(t, err)
	} else {
		db.SetMaxOpenConns(8)
	}
	t.Cleanup(func() {
		_, e := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, e)
		require.NoError(t, db.Close())
	})
	// Minimal existing prerequisites mirror users.id/email/role/deleted_at and
	// the actual settings schema. Sales repository calls below are production Go.
	_, err = db.Exec(`CREATE TABLE users(id BIGSERIAL PRIMARY KEY,email TEXT NOT NULL,role VARCHAR(20) NOT NULL DEFAULT 'user',deleted_at TIMESTAMPTZ);
CREATE TABLE settings(id BIGSERIAL PRIMARY KEY,key VARCHAR(100) NOT NULL UNIQUE,value TEXT NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());`)
	require.NoError(t, err)
	migrationPath, err := filepath.Abs(filepath.Join("..", "..", "migrations", "235_custom_sales.sql"))
	require.NoError(t, err)
	migration, err := os.ReadFile(migrationPath)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = db.Exec(string(migration))
		require.NoError(t, err, "migration run %d", i+1)
	}
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Logf("real SQL backend: %s", version)
	return db, client, NewSalesRepository(client, db), isPGlite
}

func salesRealFixture(t *testing.T, db *sql.DB, repo service.SalesRepository) (*service.SalesPartner, int64) {
	t.Helper()
	ctx := context.Background()
	var owner, customer int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email) VALUES('owner@example.com') RETURNING id`).Scan(&owner))
	require.NoError(t, db.QueryRow(`INSERT INTO users(email) VALUES('customer@example.com') RETURNING id`).Scan(&customer))
	p, err := repo.SavePartner(ctx, 0, service.SalesPartnerInput{UserID: owner, Name: "Sales A", Code: "sales-a", Hostname: "a.example.com", CommissionRate: 50, PromotionEnabled: true, AccrualEnabled: true}, owner)
	require.NoError(t, err)
	require.NoError(t, repo.BindCustomer(ctx, customer, &service.SalesAttribution{PartnerID: p.ID, Code: p.Code, ExpiresAt: time.Now().Add(time.Hour).Unix()}))
	return p, customer
}

func salesRealEvent(t *testing.T, db *sql.DB, p *service.SalesPartner, customer int64, key string, revenue, cost string, at time.Time) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO sales_commission_events(request_id,api_key_id,customer_user_id,partner_id,rule_id,revenue,cost,profit,commission_rate,commission,model,occurred_at)
SELECT $1,1,$2,$3,id,$4::numeric,$5::numeric,$4::numeric-$5::numeric,commission_rate,ROUND(($4::numeric-$5::numeric)*commission_rate/100,8),'test',$6
FROM sales_commission_rules WHERE partner_id=$3 ORDER BY id DESC LIMIT 1`, key, customer, p.ID, revenue, cost, at)
	require.NoError(t, err)
}

func TestSalesDatabaseLifecycle(t *testing.T) {
	db, _, repo, _ := salesRealDatabase(t)
	ctx := context.Background()
	p, customer := salesRealFixture(t, db, repo)
	cfg, err := repo.GetSettings(ctx)
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.NoError(t, repo.SaveSettings(ctx, &service.SalesSettings{Enabled: true, MainFrontendURL: "https://main.example.com"}, p.UserID))
	partners, count, err := repo.ListPartners(ctx, service.SalesFilter{Page: 1, PageSize: 20, Search: "Sales"})
	require.NoError(t, err)
	require.Len(t, partners, 1)
	require.EqualValues(t, 1, count)
	janCutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	salesRealEvent(t, db, p, customer, "loss-request", "1", "5", janCutoff.Add(-time.Hour))
	in := service.SalesSettlementInput{PartnerID: p.ID, Month: "2026-01", RequestKey: "january-request"}
	_, err = repo.CreateSettlement(ctx, in, janCutoff, p.UserID)
	require.ErrorIs(t, err, service.ErrSalesPendingEvents)
	n, err := repo.ProcessEvents(ctx, 250)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = repo.ProcessEvents(ctx, 250)
	require.NoError(t, err)
	require.Zero(t, n)
	jan, err := repo.CreateSettlement(ctx, in, janCutoff, p.UserID)
	require.NoError(t, err)
	require.Equal(t, -2.0, jan.CarryAmount)
	require.Zero(t, jan.PayoutAmount)
	retry, err := repo.CreateSettlement(ctx, in, janCutoff, p.UserID)
	require.NoError(t, err)
	require.Equal(t, jan.ID, retry.ID)
	salesRealEvent(t, db, p, customer, "gain-request", "20", "8", janCutoff.Add(time.Hour))
	_, err = repo.ProcessEvents(ctx, 250)
	require.NoError(t, err)
	feb, err := repo.CreateSettlement(ctx, service.SalesSettlementInput{PartnerID: p.ID, Month: "2026-02", RequestKey: "february-request"}, janCutoff.AddDate(0, 1, 0), p.UserID)
	require.NoError(t, err)
	require.Equal(t, 4.0, feb.NetCommission)
	require.Equal(t, 4.0, feb.PayoutAmount)
	_, err = repo.ConfirmSettlement(ctx, feb.ID, "confirm-february", p.UserID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE sales_partners SET payout_frozen=true WHERE id=$1`, p.ID)
	require.NoError(t, err)
	pay := service.SalesPaymentInput{RequestKey: "payment-february", PaymentReference: "manual-proof"}
	_, err = repo.PaySettlement(ctx, feb.ID, pay, p.UserID)
	require.ErrorIs(t, err, service.ErrSalesPayoutFrozen)
	_, err = db.Exec(`UPDATE sales_partners SET payout_frozen=false WHERE id=$1`, p.ID)
	require.NoError(t, err)
	paid, err := repo.PaySettlement(ctx, feb.ID, pay, p.UserID)
	require.NoError(t, err)
	require.Equal(t, "paid", paid.Status)
	repaid, err := repo.PaySettlement(ctx, feb.ID, pay, p.UserID)
	require.NoError(t, err)
	require.Equal(t, paid.PaidAt, repaid.PaidAt)
	ledger, _, err := repo.ListLedger(ctx, service.SalesFilter{PartnerID: p.ID, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, ledger, 3)
	source := ledger[0].ID
	adjIn := service.SalesAdjustmentInput{PartnerID: p.ID, SourceLedgerID: &source, Commission: -1.23456789, Note: "Refund", RequestKey: "adjustment-refund"}
	adj, err := repo.Adjust(ctx, adjIn, p.UserID)
	require.NoError(t, err)
	require.NotNil(t, adj.CustomerUserID)
	adjAgain, err := repo.Adjust(ctx, adjIn, p.UserID)
	require.NoError(t, err)
	require.Equal(t, adj.ID, adjAgain.ID)
	customers, total, err := repo.ListCustomers(ctx, service.SalesFilter{PartnerID: p.ID, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, customers, 1)
	overview, err := repo.Overview(ctx, p.ID, service.SalesFilter{})
	require.NoError(t, err)
	require.Equal(t, 4.0, overview.PaidCommission)
	require.InDelta(t, 2.76543211, overview.Commission, 1e-10)
	settlements, total, err := repo.ListSettlements(ctx, service.SalesFilter{PartnerID: p.ID, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, settlements, 2)
	_, err = db.Exec(`UPDATE sales_commission_ledger SET commission=0 WHERE id=$1`, adj.ID)
	require.Error(t, err)
	_, err = db.Exec(`DELETE FROM sales_commission_events`)
	require.Error(t, err)
}

func TestSalesDatabaseConcurrentClose(t *testing.T) {
	db, _, repo, isPGlite := salesRealDatabase(t)
	if isPGlite {
		t.Skip("PGlite multiplexes one PostgreSQL connection; native concurrent row-lock verification requires portable PostgreSQL")
	}
	ctx := context.Background()
	p, customer := salesRealFixture(t, db, repo)
	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	salesRealEvent(t, db, p, customer, "concurrent-request", "10", "2", cutoff.Add(-time.Hour))
	_, err := repo.ProcessEvents(ctx, 250)
	require.NoError(t, err)
	const attempts = 6
	results := make(chan *service.SalesSettlement, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := repo.CreateSettlement(ctx, service.SalesSettlementInput{PartnerID: p.ID, Month: "2026-01", RequestKey: "concurrent-close"}, cutoff, p.UserID)
			results <- v
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var id int64
	for v := range results {
		if id == 0 {
			id = v.ID
		}
		require.Equal(t, id, v.ID)
		require.Equal(t, 4.0, v.PayoutAmount)
	}
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sales_settlements`).Scan(&count))
	require.Equal(t, 1, count)
	// The billing producer takes FOR SHARE on its partner before inserting its
	// event. Closing must wait for that transaction, not read a partial cutoff.
	lock, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	var lockedID int64
	require.NoError(t, lock.QueryRow(`SELECT id FROM sales_partners WHERE id=$1 FOR SHARE`, p.ID).Scan(&lockedID))
	blocked := make(chan error, 1)
	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go func() {
		_, e := repo.CreateSettlement(closeCtx, service.SalesSettlementInput{PartnerID: p.ID, Month: "2026-02", RequestKey: "close-after-billing"}, cutoff.AddDate(0, 1, 0), p.UserID)
		blocked <- e
	}()
	select {
	case e := <-blocked:
		_ = lock.Rollback()
		t.Fatalf("settlement bypassed active billing lock: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, lock.Commit())
	require.NoError(t, <-blocked)
}

func TestSalesDatabaseCustomerIndex(t *testing.T) {
	db, _, repo, _ := salesRealDatabase(t)
	p, customer := salesRealFixture(t, db, repo)
	_, err := db.Exec(`INSERT INTO users(email) SELECT 'index-customer-'||n||'@example.com' FROM generate_series(1,99) n`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sales_commission_ledger(partner_id,customer_user_id,kind,commission)
SELECT $1,$2+((n-1)%100),'adjustment',0.01 FROM generate_series(1,50000) n`, p.ID, customer)
	require.NoError(t, err)
	_, err = db.Exec(`ANALYZE sales_commission_ledger`)
	require.NoError(t, err)
	rows, err := db.Query(`EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT SUM(revenue),SUM(profit),SUM(commission)
FROM sales_commission_ledger WHERE partner_id=$1 AND customer_user_id=$2`, p.ID, customer)
	require.NoError(t, err)
	var plan []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan = append(plan, line)
	}
	require.NoError(t, rows.Close())
	require.NoError(t, rows.Err())
	text := strings.Join(plan, "\n")
	t.Log(text)
	require.Contains(t, text, "idx_sales_ledger_customer_partner", "customer totals must not rescan the whole partner ledger for every listed customer")
}
