package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type salesRepository struct{ client *dbent.Client }

func NewSalesRepository(client *dbent.Client, _ *sql.DB) service.SalesRepository {
	return &salesRepository{client: client}
}

func (r *salesRepository) withTx(ctx context.Context, fn func(context.Context, *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err = fn(txCtx, tx.Client()); err != nil {
		return translatePersistenceError(err, service.ErrSalesNotFound, service.ErrSalesConflict)
	}
	return tx.Commit()
}

func salesAudit(ctx context.Context, client *dbent.Client, actor int64, action string, target int64, detail any) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = client.ExecContext(ctx, `INSERT INTO sales_audit_logs(actor_user_id,action,target_id,detail) VALUES($1,$2,NULLIF($3,0),$4::jsonb)`, actor, action, target, string(data))
	return err
}

func (r *salesRepository) GetSettings(ctx context.Context) (*service.SalesSettings, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.SalesSettingKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cfg := &service.SalesSettings{}
	if !rows.Next() {
		return cfg, rows.Err()
	}
	var value string
	if err = rows.Scan(&value); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(value), cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (r *salesRepository) SaveSettings(ctx context.Context, cfg *service.SalesSettings, actor int64) error {
	return r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		data, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if _, err = client.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, service.SalesSettingKey, string(data)); err != nil {
			return err
		}
		return salesAudit(ctx, client, actor, "settings.update", 0, cfg)
	})
}

const salesPartnerColumns = `id,user_id,name,code,hostname,commission_rate,promotion_enabled,accrual_enabled,payout_frozen,created_at,updated_at`

func scanSalesPartner(rows *sql.Rows) (*service.SalesPartner, error) {
	p := &service.SalesPartner{}
	err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Code, &p.Hostname, &p.CommissionRate, &p.PromotionEnabled, &p.AccrualEnabled, &p.PayoutFrozen, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func salesGetPartner(ctx context.Context, client *dbent.Client, condition string, value any, lock bool) (*service.SalesPartner, error) {
	query := `SELECT ` + salesPartnerColumns + ` FROM sales_partners WHERE ` + condition
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, value)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrSalesNotFound
	}
	return scanSalesPartner(rows)
}
func (r *salesRepository) GetPartner(ctx context.Context, id int64) (*service.SalesPartner, error) {
	return salesGetPartner(ctx, clientFromContext(ctx, r.client), `id=$1`, id, false)
}
func (r *salesRepository) GetPartnerByUser(ctx context.Context, id int64) (*service.SalesPartner, error) {
	return salesGetPartner(ctx, clientFromContext(ctx, r.client), `user_id=$1`, id, false)
}
func (r *salesRepository) GetPartnerByCode(ctx context.Context, code string) (*service.SalesPartner, error) {
	return salesGetPartner(ctx, clientFromContext(ctx, r.client), `code=$1`, code, false)
}

func (r *salesRepository) ListPartners(ctx context.Context, f service.SalesFilter) ([]service.SalesPartner, int64, error) {
	client := clientFromContext(ctx, r.client)
	args := []any{"%" + f.Search + "%"}
	where := ` WHERE ($1='%%' OR name ILIKE $1 OR code ILIKE $1 OR hostname ILIKE $1)`
	total, err := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_partners`+where, args...)
	if err != nil {
		return nil, 0, err
	}
	rows, err := client.QueryContext(ctx, `SELECT `+salesPartnerColumns+` FROM sales_partners`+where+` ORDER BY id DESC LIMIT $2 OFFSET $3`, args[0], f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.SalesPartner, 0)
	for rows.Next() {
		p, err := scanSalesPartner(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *p)
	}
	return items, total, rows.Err()
}

func (r *salesRepository) SavePartner(ctx context.Context, id int64, in service.SalesPartnerInput, actor int64) (out *service.SalesPartner, err error) {
	err = r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		var old *service.SalesPartner
		if id > 0 {
			var e error
			old, e = salesGetPartner(ctx, client, `id=$1`, id, true)
			if e != nil {
				return e
			}
			if old.UserID != in.UserID {
				return service.ErrSalesInvalid
			}
		}
		active, e := salesCount(ctx, client, `SELECT COUNT(*) FROM users WHERE id=$1 AND deleted_at IS NULL AND role='user'`, in.UserID)
		if e != nil {
			return e
		}
		if active != 1 {
			return service.ErrSalesInvalid
		}
		var rows *sql.Rows
		if id == 0 {
			rows, e = client.QueryContext(ctx, `INSERT INTO sales_partners(user_id,name,code,hostname,commission_rate,promotion_enabled,accrual_enabled,payout_frozen) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+salesPartnerColumns, in.UserID, in.Name, in.Code, in.Hostname, in.CommissionRate, in.PromotionEnabled, in.AccrualEnabled, in.PayoutFrozen)
		} else {
			rows, e = client.QueryContext(ctx, `UPDATE sales_partners SET name=$2,code=$3,hostname=$4,commission_rate=$5,promotion_enabled=$6,accrual_enabled=$7,payout_frozen=$8,updated_at=NOW() WHERE id=$1 RETURNING `+salesPartnerColumns, id, in.Name, in.Code, in.Hostname, in.CommissionRate, in.PromotionEnabled, in.AccrualEnabled, in.PayoutFrozen)
		}
		if e != nil {
			return e
		}
		if !rows.Next() {
			_ = rows.Close()
			return service.ErrSalesNotFound
		}
		out, e = scanSalesPartner(rows)
		_ = rows.Close()
		if e != nil {
			return e
		}
		if old == nil || old.CommissionRate != in.CommissionRate {
			if _, e = client.ExecContext(ctx, `INSERT INTO sales_commission_rules(partner_id,commission_rate,actor_user_id) VALUES($1,$2,$3)`, out.ID, in.CommissionRate, actor); e != nil {
				return e
			}
		}
		return salesAudit(ctx, client, actor, "partner.save", out.ID, map[string]any{"before": old, "after": out})
	})
	return
}

func (r *salesRepository) BindCustomer(ctx context.Context, userID int64, a *service.SalesAttribution) error {
	return r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		p, err := salesGetPartner(ctx, client, `id=$1`, a.PartnerID, true)
		if err != nil {
			return err
		}
		if !p.PromotionEnabled || p.Code != a.Code || p.UserID == userID {
			return service.ErrSalesInvalid
		}
		rows, err := client.QueryContext(ctx, `INSERT INTO sales_customers(user_id,partner_id,referral_code) VALUES($1,$2,$3) ON CONFLICT(user_id) DO NOTHING RETURNING user_id`, userID, p.ID, p.Code)
		if err != nil {
			return err
		}
		inserted := rows.Next()
		_ = rows.Close()
		if !inserted {
			count, err := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_customers WHERE user_id=$1 AND partner_id=$2`, userID, p.ID)
			if err != nil {
				return err
			}
			if count == 0 {
				return service.ErrSalesConflict
			}
		}
		return nil
	})
}

func salesCount(ctx context.Context, client *dbent.Client, query string, args ...any) (int64, error) {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var count int64
	if rows.Next() {
		err = rows.Scan(&count)
	}
	if err == nil {
		err = rows.Err()
	}
	return count, err
}

func salesWhere(f service.SalesFilter, alias, timeColumn string) (string, []any) {
	where := []string{"TRUE"}
	args := make([]any, 0)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.PartnerID > 0 {
		add(alias+`.partner_id=$%d`, f.PartnerID)
	}
	if f.StartAt != nil {
		add(timeColumn+` >= $%d`, *f.StartAt)
	}
	if f.EndAt != nil {
		add(timeColumn+` < $%d`, *f.EndAt)
	}
	return ` WHERE ` + strings.Join(where, ` AND `), args
}

func salesPage(query string, args []any, f service.SalesFilter) (string, []any) {
	n := len(args)
	return query + fmt.Sprintf(` LIMIT $%d OFFSET $%d`, n+1, n+2), append(args, f.PageSize, (f.Page-1)*f.PageSize)
}

func (r *salesRepository) ListCustomers(ctx context.Context, f service.SalesFilter) ([]service.SalesCustomer, int64, error) {
	client := clientFromContext(ctx, r.client)
	where, args := salesWhere(f, "sc", "sc.created_at")
	total, err := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_customers sc`+where, args...)
	if err != nil {
		return nil, 0, err
	}
	query, args := salesPage(`SELECT sc.user_id,sc.partner_id,COALESCE(u.email,''),sc.created_at,COALESCE(x.revenue,0),COALESCE(x.profit,0),COALESCE(x.commission,0)
FROM sales_customers sc LEFT JOIN users u ON u.id=sc.user_id
LEFT JOIN LATERAL (SELECT SUM(revenue) revenue,SUM(profit) profit,SUM(commission) commission FROM sales_commission_ledger l WHERE l.customer_user_id=sc.user_id AND l.partner_id=sc.partner_id) x ON TRUE`+where+` ORDER BY sc.created_at DESC,sc.user_id DESC`, args, f)
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.SalesCustomer, 0)
	for rows.Next() {
		var v service.SalesCustomer
		if err = rows.Scan(&v.UserID, &v.PartnerID, &v.Email, &v.CreatedAt, &v.Revenue, &v.Profit, &v.Commission); err != nil {
			return nil, 0, err
		}
		items = append(items, v)
	}
	return items, total, rows.Err()
}

const salesLedgerColumns = `l.id,l.partner_id,l.customer_user_id,l.event_id,l.kind,l.revenue,l.cost,l.profit,l.commission_rate,l.commission,l.currency,l.note,l.occurred_at,si.settlement_id`

func scanSalesLedger(rows *sql.Rows) (*service.SalesLedgerEntry, error) {
	v := &service.SalesLedgerEntry{}
	err := rows.Scan(&v.ID, &v.PartnerID, &v.CustomerUserID, &v.EventID, &v.Kind, &v.Revenue, &v.Cost, &v.Profit, &v.CommissionRate, &v.Commission, &v.Currency, &v.Note, &v.OccurredAt, &v.SettlementID)
	return v, err
}

func (r *salesRepository) ListLedger(ctx context.Context, f service.SalesFilter) ([]service.SalesLedgerEntry, int64, error) {
	client := clientFromContext(ctx, r.client)
	where, args := salesWhere(f, "l", "l.occurred_at")
	total, err := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_commission_ledger l`+where, args...)
	if err != nil {
		return nil, 0, err
	}
	query, args := salesPage(`SELECT `+salesLedgerColumns+` FROM sales_commission_ledger l LEFT JOIN sales_settlement_items si ON si.ledger_id=l.id`+where+` ORDER BY l.occurred_at DESC,l.id DESC`, args, f)
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.SalesLedgerEntry, 0)
	for rows.Next() {
		v, e := scanSalesLedger(rows)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, *v)
	}
	return items, total, rows.Err()
}

func (r *salesRepository) Overview(ctx context.Context, id int64, f service.SalesFilter) (*service.SalesOverview, error) {
	client := clientFromContext(ctx, r.client)
	p, err := r.GetPartner(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &service.SalesOverview{Partner: p}
	f.PartnerID = id
	where, args := salesWhere(f, "l", "l.occurred_at")
	rows, err := client.QueryContext(ctx, `SELECT COALESCE(SUM(l.revenue),0),COALESCE(SUM(l.cost),0),COALESCE(SUM(l.profit),0),COALESCE(SUM(l.commission),0) FROM sales_commission_ledger l`+where+` AND l.kind <> 'carry'`, args...)
	if err != nil {
		return nil, err
	}
	if rows.Next() {
		err = rows.Scan(&out.Revenue, &out.Cost, &out.Profit, &out.Commission)
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = client.QueryContext(ctx, `SELECT
(SELECT COUNT(*) FROM sales_customers WHERE partner_id=$1),
(SELECT COALESCE(SUM(l.commission),0) FROM sales_commission_ledger l WHERE partner_id=$1 AND NOT EXISTS(SELECT 1 FROM sales_settlement_items si WHERE si.ledger_id=l.id)),
(SELECT COALESCE(SUM(payout_amount),0) FROM sales_settlements WHERE partner_id=$1 AND status IN ('draft','confirmed')),
(SELECT COALESCE(SUM(payout_amount),0) FROM sales_settlements WHERE partner_id=$1 AND status='paid'),
(SELECT COUNT(*) FROM sales_commission_events WHERE partner_id=$1 AND processed_at IS NULL)`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		err = rows.Scan(&out.CustomerCount, &out.UnsettledCommission, &out.PendingPayout, &out.PaidCommission, &out.PendingEvents)
	}
	return out, err
}

// Both event processing and settlement closure lock the partner before claiming
// rows. The same order is required of the billing event producer.
func (r *salesRepository) ProcessEvents(ctx context.Context, limit int) (processed int64, err error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	err = r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		rows, e := client.QueryContext(ctx, `SELECT p.id FROM sales_partners p WHERE EXISTS(SELECT 1 FROM sales_commission_events e WHERE e.partner_id=p.id AND e.processed_at IS NULL) ORDER BY (SELECT MIN(e.id) FROM sales_commission_events e WHERE e.partner_id=p.id AND e.processed_at IS NULL),p.id LIMIT 1 FOR UPDATE OF p SKIP LOCKED`)
		if e != nil {
			return e
		}
		var id int64
		if !rows.Next() {
			_ = rows.Close()
			return nil
		}
		e = rows.Scan(&id)
		_ = rows.Close()
		if e != nil {
			return e
		}
		rows, e = client.QueryContext(ctx, `WITH selected AS (
 SELECT e.* FROM sales_commission_events e WHERE partner_id=$1 AND processed_at IS NULL ORDER BY id LIMIT $2 FOR UPDATE
), inserted AS (
 INSERT INTO sales_commission_ledger(partner_id,customer_user_id,event_id,kind,revenue,cost,profit,commission_rate,commission,currency,occurred_at)
 SELECT partner_id,customer_user_id,id,'usage',revenue,cost,profit,commission_rate,commission,currency,occurred_at FROM selected
 ON CONFLICT(event_id) DO NOTHING RETURNING id
), completed AS (
 UPDATE sales_commission_events SET processed_at=NOW() WHERE id IN (SELECT id FROM selected) RETURNING id
) SELECT COUNT(*) FROM completed`, id, limit)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }()
		if rows.Next() {
			e = rows.Scan(&processed)
		}
		return e
	})
	return
}

const salesSettlementColumns = `id,partner_id,month,cutoff,net_commission,payout_amount,carry_amount,status,currency,payment_reference,created_at,confirmed_at,paid_at`

func scanSalesSettlement(rows *sql.Rows) (*service.SalesSettlement, error) {
	v := &service.SalesSettlement{}
	err := rows.Scan(&v.ID, &v.PartnerID, &v.Month, &v.Cutoff, &v.NetCommission, &v.PayoutAmount, &v.CarryAmount, &v.Status, &v.Currency, &v.PaymentReference, &v.CreatedAt, &v.ConfirmedAt, &v.PaidAt)
	return v, err
}

func salesGetSettlement(ctx context.Context, client *dbent.Client, id int64) (*service.SalesSettlement, error) {
	rows, err := client.QueryContext(ctx, `SELECT `+salesSettlementColumns+` FROM sales_settlements WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, service.ErrSalesNotFound
	}
	return scanSalesSettlement(rows)
}

func (r *salesRepository) ListSettlements(ctx context.Context, f service.SalesFilter) ([]service.SalesSettlement, int64, error) {
	client := clientFromContext(ctx, r.client)
	where, args := salesWhere(f, "s", "s.created_at")
	total, err := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_settlements s`+where, args...)
	if err != nil {
		return nil, 0, err
	}
	query, args := salesPage(`SELECT `+salesSettlementColumns+` FROM sales_settlements s`+where+` ORDER BY created_at DESC,id DESC`, args, f)
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.SalesSettlement, 0)
	for rows.Next() {
		v, e := scanSalesSettlement(rows)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, *v)
	}
	return items, total, rows.Err()
}

func (r *salesRepository) CreateSettlement(ctx context.Context, in service.SalesSettlementInput, cutoff time.Time, actor int64) (out *service.SalesSettlement, err error) {
	err = r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		if _, e := salesGetPartner(ctx, client, `id=$1`, in.PartnerID, true); e != nil {
			return e
		}
		rows, e := client.QueryContext(ctx, `SELECT id,partner_id,month FROM sales_settlements WHERE request_key=$1`, in.RequestKey)
		if e != nil {
			return e
		}
		if rows.Next() {
			var id, pid int64
			var month string
			e = rows.Scan(&id, &pid, &month)
			_ = rows.Close()
			if e != nil {
				return e
			}
			if pid != in.PartnerID || month != in.Month {
				return service.ErrSalesConflict
			}
			out, e = salesGetSettlement(ctx, client, id)
			return e
		}
		_ = rows.Close()
		count, e := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_settlements WHERE partner_id=$1 AND cutoff >= $2`, in.PartnerID, cutoff)
		if e != nil {
			return e
		}
		if count > 0 {
			return service.ErrSalesConflict
		}
		count, e = salesCount(ctx, client, `SELECT COUNT(*) FROM sales_commission_events WHERE partner_id=$1 AND occurred_at < $2 AND processed_at IS NULL`, in.PartnerID, cutoff)
		if e != nil {
			return e
		}
		if count > 0 {
			return service.ErrSalesPendingEvents
		}
		// Claim the entire signed balance before the cutoff, including prior carry.
		// Never clamp individual request profits; only a net payout is nonnegative.
		rows, e = client.QueryContext(ctx, `WITH totals AS (
 SELECT COALESCE(SUM(l.commission),0) amount FROM sales_commission_ledger l
 WHERE l.partner_id=$1 AND l.occurred_at < $2 AND NOT EXISTS(SELECT 1 FROM sales_settlement_items si WHERE si.ledger_id=l.id)
) INSERT INTO sales_settlements(partner_id,month,cutoff,net_commission,payout_amount,carry_amount,request_key,actor_user_id)
 SELECT $1,$3,$2,amount,GREATEST(amount,0),LEAST(amount,0),$4,$5 FROM totals RETURNING `+salesSettlementColumns, in.PartnerID, cutoff, in.Month, in.RequestKey, actor)
		if e != nil {
			return e
		}
		if !rows.Next() {
			_ = rows.Close()
			return service.ErrSalesConflict
		}
		out, e = scanSalesSettlement(rows)
		_ = rows.Close()
		if e != nil {
			return e
		}
		if _, e = client.ExecContext(ctx, `INSERT INTO sales_settlement_items(ledger_id,settlement_id) SELECT l.id,$3 FROM sales_commission_ledger l WHERE l.partner_id=$1 AND l.occurred_at < $2 AND NOT EXISTS(SELECT 1 FROM sales_settlement_items si WHERE si.ledger_id=l.id)`, in.PartnerID, cutoff, out.ID); e != nil {
			return e
		}
		// Copy NUMERIC directly: a float64 round trip could lose the last ledger
		// digits for large signed carry amounts.
		if _, e = client.ExecContext(ctx, `INSERT INTO sales_commission_ledger(partner_id,kind,commission,note,request_key,occurred_at,actor_user_id) SELECT partner_id,'carry',carry_amount,'Prior settlement loss carry',$2,cutoff,$3 FROM sales_settlements WHERE id=$1 AND carry_amount<0`, out.ID, fmt.Sprintf("carry:%d", out.ID), actor); e != nil {
			return e
		}
		return salesAudit(ctx, client, actor, "settlement.create", out.ID, out)
	})
	return
}

func (r *salesRepository) ConfirmSettlement(ctx context.Context, id int64, key string, actor int64) (out *service.SalesSettlement, err error) {
	err = r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		v, e := salesGetSettlement(ctx, client, id)
		if e != nil {
			return e
		}
		if _, e = salesGetPartner(ctx, client, `id=$1`, v.PartnerID, true); e != nil {
			return e
		}
		rows, e := client.QueryContext(ctx, `SELECT status,COALESCE(confirm_request_key,'') FROM sales_settlements WHERE id=$1 FOR UPDATE`, id)
		if e != nil {
			return e
		}
		var status, existing string
		if rows.Next() {
			e = rows.Scan(&status, &existing)
		}
		_ = rows.Close()
		if e != nil {
			return e
		}
		if existing != "" {
			if existing != key {
				return service.ErrSalesConflict
			}
			out, e = salesGetSettlement(ctx, client, id)
			return e
		}
		if status != "draft" {
			return service.ErrSalesConflict
		}
		if _, e = client.ExecContext(ctx, `UPDATE sales_settlements SET status='confirmed',confirm_request_key=$2,confirmed_at=NOW() WHERE id=$1`, id, key); e != nil {
			return e
		}
		out, e = salesGetSettlement(ctx, client, id)
		if e != nil {
			return e
		}
		return salesAudit(ctx, client, actor, "settlement.confirm", id, out)
	})
	return
}

func (r *salesRepository) PaySettlement(ctx context.Context, id int64, in service.SalesPaymentInput, actor int64) (out *service.SalesSettlement, err error) {
	err = r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		v, e := salesGetSettlement(ctx, client, id)
		if e != nil {
			return e
		}
		p, e := salesGetPartner(ctx, client, `id=$1`, v.PartnerID, true)
		if e != nil {
			return e
		}
		rows, e := client.QueryContext(ctx, `SELECT status,COALESCE(pay_request_key,''),payment_reference FROM sales_settlements WHERE id=$1 FOR UPDATE`, id)
		if e != nil {
			return e
		}
		var status, key, ref string
		if rows.Next() {
			e = rows.Scan(&status, &key, &ref)
		}
		_ = rows.Close()
		if e != nil {
			return e
		}
		if key != "" {
			if key != in.RequestKey || ref != in.PaymentReference {
				return service.ErrSalesConflict
			}
			out, e = salesGetSettlement(ctx, client, id)
			return e
		}
		if p.PayoutFrozen {
			return service.ErrSalesPayoutFrozen
		}
		if status != "confirmed" {
			return service.ErrSalesConflict
		}
		if _, e = client.ExecContext(ctx, `UPDATE sales_settlements SET status='paid',pay_request_key=$2,payment_reference=$3,paid_at=NOW() WHERE id=$1`, id, in.RequestKey, in.PaymentReference); e != nil {
			return e
		}
		out, e = salesGetSettlement(ctx, client, id)
		if e != nil {
			return e
		}
		return salesAudit(ctx, client, actor, "settlement.pay", id, out)
	})
	return
}

func (r *salesRepository) Adjust(ctx context.Context, in service.SalesAdjustmentInput, actor int64) (out *service.SalesLedgerEntry, err error) {
	err = r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		if _, e := salesGetPartner(ctx, client, `id=$1`, in.PartnerID, true); e != nil {
			return e
		}
		rows, e := client.QueryContext(ctx, `SELECT id,partner_id,commission,note,source_ledger_id FROM sales_commission_ledger WHERE request_key=$1`, in.RequestKey)
		if e != nil {
			return e
		}
		var id int64
		if rows.Next() {
			var pid int64
			var amount float64
			var note string
			var source *int64
			e = rows.Scan(&id, &pid, &amount, &note, &source)
			_ = rows.Close()
			if e != nil {
				return e
			}
			sameSource := (source == nil && in.SourceLedgerID == nil) || (source != nil && in.SourceLedgerID != nil && *source == *in.SourceLedgerID)
			if pid != in.PartnerID || amount != in.Commission || note != in.Note || !sameSource {
				return service.ErrSalesConflict
			}
		} else {
			_ = rows.Close()
			if in.SourceLedgerID != nil {
				count, e := salesCount(ctx, client, `SELECT COUNT(*) FROM sales_commission_ledger WHERE id=$1 AND partner_id=$2 AND kind<>'carry'`, *in.SourceLedgerID, in.PartnerID)
				if e != nil {
					return e
				}
				if count != 1 {
					return service.ErrSalesInvalid
				}
			}
			rows, e = client.QueryContext(ctx, `INSERT INTO sales_commission_ledger(partner_id,source_ledger_id,customer_user_id,kind,commission,note,request_key,actor_user_id) VALUES($1,$2,(SELECT customer_user_id FROM sales_commission_ledger WHERE id=$2),'adjustment',$3,$4,$5,$6) RETURNING id`, in.PartnerID, in.SourceLedgerID, in.Commission, in.Note, in.RequestKey, actor)
			if e != nil {
				return e
			}
			if rows.Next() {
				e = rows.Scan(&id)
			}
			_ = rows.Close()
			if e != nil {
				return e
			}
			if e = salesAudit(ctx, client, actor, "commission.adjust", id, in); e != nil {
				return e
			}
		}
		rows, e = client.QueryContext(ctx, `SELECT `+salesLedgerColumns+` FROM sales_commission_ledger l LEFT JOIN sales_settlement_items si ON si.ledger_id=l.id WHERE l.id=$1`, id)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			return service.ErrSalesNotFound
		}
		out, e = scanSalesLedger(rows)
		return e
	})
	return
}
