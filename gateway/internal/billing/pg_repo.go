package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// PgOrderRepo 是 OrderRepo 的 PostgreSQL 实现。
type PgOrderRepo struct {
	pool *pgxpool.Pool
}

func NewPgOrderRepo(pool *pgxpool.Pool) *PgOrderRepo {
	return &PgOrderRepo{pool: pool}
}

func (r *PgOrderRepo) Create(ctx context.Context, o *Order) error {
	const q = `
		INSERT INTO billing.orders (id, user_id, plan_id, amount_cents, currency, status,
		    payment_provider, external_order_id, idempotency_key, created_at, expires_at, purpose,
		    original_currency, original_amount_cents, fx_rate)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`
	amountCents := o.Amount.Mul(decimal.NewFromInt(100)).IntPart()
	createdAt := o.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	expiresAt := o.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = createdAt.Add(30 * time.Minute)
	}
	purpose := o.Purpose
	if purpose == "" {
		purpose = OrderPurposeSubscription
	}
	// FX 快照：三个字段同时为零值 ⇒ 未发生跨币换算，写 NULL；
	// 否则把原始金额按 cents 落库，保留 fx_rate 的全精度。
	var origCurrency any
	var origAmountCents any
	var fxRate any
	if o.OriginalCurrency != "" && !o.OriginalAmount.IsZero() {
		origCurrency = o.OriginalCurrency
		origAmountCents = o.OriginalAmount.Mul(decimal.NewFromInt(100)).IntPart()
		fxRate = o.FXRate.String() // pgx NUMERIC 接受字符串，避免 float 精度
	}
	_, err := r.pool.Exec(ctx, q,
		o.ID, o.UserID, o.PlanID, amountCents, o.Currency, string(o.Status),
		o.PaymentProvider, nullStr(o.ExternalOrderID), nullStr(o.IdempotencyKey),
		createdAt, expiresAt, purpose,
		origCurrency, origAmountCents, fxRate,
	)
	if err != nil {
		return fmt.Errorf("billing.PgOrderRepo.Create: %w", err)
	}
	o.CreatedAt = createdAt
	o.ExpiresAt = expiresAt
	o.Purpose = purpose
	return nil
}

// orderSelectCols 是所有 SELECT 共用的列清单（含 FX 快照三列；NULL 用 COALESCE 落到零值）。
const orderSelectCols = `
	id, user_id, plan_id, amount_cents, currency, status,
	COALESCE(payment_provider,''), COALESCE(external_order_id,''),
	COALESCE(idempotency_key,''),
	created_at, COALESCE(paid_at, 'epoch'::timestamptz),
	COALESCE(expires_at, 'epoch'::timestamptz),
	COALESCE(purpose, 'subscription'),
	COALESCE(original_currency, ''),
	COALESCE(original_amount_cents, 0),
	COALESCE(fx_rate, 0::numeric)
`

func (r *PgOrderRepo) Get(ctx context.Context, id string) (*Order, error) {
	q := `SELECT ` + orderSelectCols + ` FROM billing.orders WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	return scanOrder(row)
}

func (r *PgOrderRepo) GetByIdempotency(ctx context.Context, idem string) (*Order, error) {
	q := `SELECT ` + orderSelectCols + ` FROM billing.orders WHERE idempotency_key = $1`
	row := r.pool.QueryRow(ctx, q, idem)
	o, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	return o, nil
}

func (r *PgOrderRepo) Update(ctx context.Context, o *Order) error {
	const q = `
		UPDATE billing.orders SET
			status = $2, payment_provider = $3, external_order_id = $4,
			paid_at = $5, updated_at = now()
		WHERE id = $1
	`
	var paidAt any
	if !o.PaidAt.IsZero() {
		paidAt = o.PaidAt
	}
	tag, err := r.pool.Exec(ctx, q,
		o.ID, string(o.Status), o.PaymentProvider, nullStr(o.ExternalOrderID), paidAt,
	)
	if err != nil {
		return fmt.Errorf("billing.PgOrderRepo.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOrderNotFound
	}
	return nil
}

func (r *PgOrderRepo) ListExpired(ctx context.Context, now time.Time) ([]*Order, error) {
	q := `SELECT ` + orderSelectCols + `
		FROM billing.orders
		WHERE expires_at < $1 AND status IN ('pending', 'paying')`
	rows, err := r.pool.Query(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("billing.PgOrderRepo.ListExpired: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (r *PgOrderRepo) ListByUser(ctx context.Context, userID string) ([]*Order, error) {
	q := `SELECT ` + orderSelectCols + `
		FROM billing.orders WHERE user_id = $1 ORDER BY created_at DESC LIMIT 100`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("billing.PgOrderRepo.ListByUser: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (r *PgOrderRepo) CreateRefund(ctx context.Context, orderID string, amount decimal.Decimal, reason, refundType, operatorID string) error {
	amountCents := amount.Mul(decimal.NewFromInt(100)).IntPart()
	const q = `
		INSERT INTO billing.refunds (order_id, amount_cents, reason, refund_type, operator_id, operator_note, status)
		VALUES ($1, $2, $3, $4, $5, '', 'completed')
	`
	var opID any
	if operatorID != "" {
		opID = operatorID
	}
	_, err := r.pool.Exec(ctx, q, orderID, amountCents, reason, refundType, opID)
	if err != nil {
		return fmt.Errorf("billing.PgOrderRepo.CreateRefund: %w", err)
	}
	return nil
}

func scanOrder(row pgx.Row) (*Order, error) {
	var (
		o                 Order
		amountCents       int64
		status            string
		paidAt, expiresAt time.Time
		purpose           string
		origCurrency      string
		origAmountCents   int64
		fxRate            decimal.Decimal
	)
	if err := row.Scan(
		&o.ID, &o.UserID, &o.PlanID, &amountCents, &o.Currency, &status,
		&o.PaymentProvider, &o.ExternalOrderID, &o.IdempotencyKey,
		&o.CreatedAt, &paidAt, &expiresAt, &purpose,
		&origCurrency, &origAmountCents, &fxRate,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	o.Amount = decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100))
	o.Status = OrderStatus(status)
	o.Purpose = purpose
	if paidAt.Unix() > 0 {
		o.PaidAt = paidAt
	}
	if expiresAt.Unix() > 0 {
		o.ExpiresAt = expiresAt
	}
	if origCurrency != "" && origAmountCents > 0 {
		o.OriginalCurrency = origCurrency
		o.OriginalAmount = decimal.NewFromInt(origAmountCents).Div(decimal.NewFromInt(100))
		o.FXRate = fxRate
	}
	return &o, nil
}

func scanOrders(rows pgx.Rows) ([]*Order, error) {
	var out []*Order
	for rows.Next() {
		var (
			o                 Order
			amountCents       int64
			status            string
			paidAt, expiresAt time.Time
			purpose           string
			origCurrency      string
			origAmountCents   int64
			fxRate            decimal.Decimal
		)
		if err := rows.Scan(
			&o.ID, &o.UserID, &o.PlanID, &amountCents, &o.Currency, &status,
			&o.PaymentProvider, &o.ExternalOrderID, &o.IdempotencyKey,
			&o.CreatedAt, &paidAt, &expiresAt, &purpose,
			&origCurrency, &origAmountCents, &fxRate,
		); err != nil {
			continue
		}
		o.Amount = decimal.NewFromInt(amountCents).Div(decimal.NewFromInt(100))
		o.Status = OrderStatus(status)
		o.Purpose = purpose
		if paidAt.Unix() > 0 {
			o.PaidAt = paidAt
		}
		if expiresAt.Unix() > 0 {
			o.ExpiresAt = expiresAt
		}
		if origCurrency != "" && origAmountCents > 0 {
			o.OriginalCurrency = origCurrency
			o.OriginalAmount = decimal.NewFromInt(origAmountCents).Div(decimal.NewFromInt(100))
			o.FXRate = fxRate
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
