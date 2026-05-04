package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 已知错误。
var (
	ErrInsufficientBalance = errors.New("balance: insufficient")
	ErrBalanceLocked       = errors.New("balance: optimistic lock conflict")
	ErrInvalidAmount       = errors.New("balance: amount must be > 0")
)

// 流水类型常量。
const (
	BalanceKindTopup        = "topup"
	BalanceKindOverage      = "overage"
	BalanceKindRefund       = "refund"
	BalanceKindAdjust       = "adjust"
	BalanceKindSubscription = "subscription"
)

// Balance 是用户余额（钱包）快照。
type Balance struct {
	UserID       string    `json:"userId"`
	BalanceCents int64     `json:"balanceCents"`
	Currency     string    `json:"currency"`
	Version      int64     `json:"version"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// BalanceTransaction 是单条流水（不可变）。
type BalanceTransaction struct {
	ID                int64     `json:"id"`
	UserID            string    `json:"userId"`
	AmountCents       int64     `json:"amountCents"` // signed
	Kind              string    `json:"kind"`
	OrderID           string    `json:"orderId,omitempty"`
	Reference         string    `json:"reference,omitempty"`
	Description       string    `json:"description,omitempty"`
	BalanceAfterCents int64     `json:"balanceAfterCents"`
	CreatedAt         time.Time `json:"createdAt"`
}

// CreditRequest 是充值/退款入参。
type CreditRequest struct {
	UserID      string
	AmountCents int64 // 必须 > 0
	Kind        string
	OrderID     string
	Reference   string
	Description string
}

// DebitRequest 是扣费入参。
type DebitRequest struct {
	UserID      string
	AmountCents int64 // 必须 > 0
	Kind        string
	OrderID     string
	Reference   string
	Description string
}

// SetRequest 是管理员"覆盖"余额到具体值时的入参。
type SetRequest struct {
	UserID         string
	NewBalanceCents int64 // 必须 >= 0
	Description    string
	Reference      string
}

// BalanceRepo 抽象余额读写。
type BalanceRepo interface {
	Get(ctx context.Context, userID string) (*Balance, error)
	Credit(ctx context.Context, req CreditRequest) (*Balance, *BalanceTransaction, error)
	Debit(ctx context.Context, req DebitRequest) (*Balance, *BalanceTransaction, error)
	// SetBalance 把用户余额直接覆盖为 NewBalanceCents（管理员调账用）；
	// 写一条 kind=adjust 的差额流水（amount = newBalance - oldBalance）。
	SetBalance(ctx context.Context, req SetRequest) (*Balance, *BalanceTransaction, error)
	ListTransactions(ctx context.Context, userID string, limit, offset int) ([]*BalanceTransaction, int, error)
}

// ---- PG impl ----

type pgBalanceRepo struct {
	pool *pgxpool.Pool
}

// NewPgBalanceRepo 构造 PG 仓库。
func NewPgBalanceRepo(pool *pgxpool.Pool) BalanceRepo {
	return &pgBalanceRepo{pool: pool}
}

func (r *pgBalanceRepo) Get(ctx context.Context, userID string) (*Balance, error) {
	const q = `
		SELECT user_id, balance_cents, currency, version, updated_at
		FROM billing.user_balances WHERE user_id = $1
	`
	var b Balance
	err := r.pool.QueryRow(ctx, q, userID).Scan(
		&b.UserID, &b.BalanceCents, &b.Currency, &b.Version, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &Balance{UserID: userID, BalanceCents: 0, Currency: "CNY"}, nil
		}
		return nil, err
	}
	return &b, nil
}

// Credit 在事务内：UPSERT user_balances + INSERT balance_transactions。
func (r *pgBalanceRepo) Credit(ctx context.Context, req CreditRequest) (*Balance, *BalanceTransaction, error) {
	if req.AmountCents <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	if req.Kind == "" {
		return nil, nil, errors.New("balance: kind required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// UPSERT user_balances，加 amount_cents 并 +1 version
	const upsertQ = `
		INSERT INTO billing.user_balances (user_id, balance_cents, currency, version, updated_at)
		VALUES ($1, $2, 'CNY', 1, now())
		ON CONFLICT (user_id) DO UPDATE SET
			balance_cents = user_balances.balance_cents + EXCLUDED.balance_cents,
			version = user_balances.version + 1,
			updated_at = now()
		RETURNING user_id, balance_cents, currency, version, updated_at
	`
	var bal Balance
	if err := tx.QueryRow(ctx, upsertQ, req.UserID, req.AmountCents).Scan(
		&bal.UserID, &bal.BalanceCents, &bal.Currency, &bal.Version, &bal.UpdatedAt); err != nil {
		return nil, nil, fmt.Errorf("balance.Credit upsert: %w", err)
	}
	// 写流水
	const txQ = `
		INSERT INTO billing.balance_transactions
			(user_id, amount_cents, kind, order_id, reference, description, balance_after_cents)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7)
		RETURNING id, user_id, amount_cents, kind, COALESCE(order_id,''), reference, description, balance_after_cents, created_at
	`
	var bt BalanceTransaction
	if err := tx.QueryRow(ctx, txQ,
		req.UserID, req.AmountCents, req.Kind, req.OrderID, req.Reference, req.Description, bal.BalanceCents,
	).Scan(&bt.ID, &bt.UserID, &bt.AmountCents, &bt.Kind, &bt.OrderID, &bt.Reference, &bt.Description, &bt.BalanceAfterCents, &bt.CreatedAt); err != nil {
		return nil, nil, fmt.Errorf("balance.Credit ledger: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &bal, &bt, nil
}

// Debit 原子扣费：当余额不足时返回 ErrInsufficientBalance。
//
// 用 row-level UPDATE ... WHERE balance_cents >= $amount 防止并发竞争扣成负数。
func (r *pgBalanceRepo) Debit(ctx context.Context, req DebitRequest) (*Balance, *BalanceTransaction, error) {
	if req.AmountCents <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	if req.Kind == "" {
		return nil, nil, errors.New("balance: kind required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 条件 UPDATE：余额够才扣
	const updQ = `
		UPDATE billing.user_balances SET
			balance_cents = balance_cents - $2,
			version = version + 1,
			updated_at = now()
		WHERE user_id = $1 AND balance_cents >= $2
		RETURNING user_id, balance_cents, currency, version, updated_at
	`
	var bal Balance
	row := tx.QueryRow(ctx, updQ, req.UserID, req.AmountCents)
	if err := row.Scan(&bal.UserID, &bal.BalanceCents, &bal.Currency, &bal.Version, &bal.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrInsufficientBalance
		}
		return nil, nil, fmt.Errorf("balance.Debit update: %w", err)
	}
	const txQ = `
		INSERT INTO billing.balance_transactions
			(user_id, amount_cents, kind, order_id, reference, description, balance_after_cents)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7)
		RETURNING id, user_id, amount_cents, kind, COALESCE(order_id,''), reference, description, balance_after_cents, created_at
	`
	var bt BalanceTransaction
	if err := tx.QueryRow(ctx, txQ,
		req.UserID, -req.AmountCents, req.Kind, req.OrderID, req.Reference, req.Description, bal.BalanceCents,
	).Scan(&bt.ID, &bt.UserID, &bt.AmountCents, &bt.Kind, &bt.OrderID, &bt.Reference, &bt.Description, &bt.BalanceAfterCents, &bt.CreatedAt); err != nil {
		return nil, nil, fmt.Errorf("balance.Debit ledger: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &bal, &bt, nil
}

// SetBalance 把余额覆盖为指定值；写一条差额 adjust 流水。
func (r *pgBalanceRepo) SetBalance(ctx context.Context, req SetRequest) (*Balance, *BalanceTransaction, error) {
	if req.NewBalanceCents < 0 {
		return nil, nil, ErrInvalidAmount
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// UPSERT：若不存在则创建，记录新余额，并算出差额。
	const upsertQ = `
		INSERT INTO billing.user_balances (user_id, balance_cents, currency, version, updated_at)
		VALUES ($1, $2, 'CNY', 1, now())
		ON CONFLICT (user_id) DO UPDATE SET
			balance_cents = EXCLUDED.balance_cents,
			version = user_balances.version + 1,
			updated_at = now()
		RETURNING user_id, balance_cents, currency, version, updated_at,
			(SELECT balance_cents FROM billing.user_balances WHERE user_id = $1) AS existing
	`
	// 上面的 RETURNING 在 INSERT 路径下 existing 为 NULL；改用先读后写的方式更稳妥
	// 这里改写：先 SELECT 取旧值，再 UPSERT。

	var oldCents int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE((SELECT balance_cents FROM billing.user_balances WHERE user_id = $1), 0)`,
		req.UserID).Scan(&oldCents); err != nil {
		return nil, nil, fmt.Errorf("balance.SetBalance read old: %w", err)
	}
	delta := req.NewBalanceCents - oldCents

	const upQ = `
		INSERT INTO billing.user_balances (user_id, balance_cents, currency, version, updated_at)
		VALUES ($1, $2, 'CNY', 1, now())
		ON CONFLICT (user_id) DO UPDATE SET
			balance_cents = EXCLUDED.balance_cents,
			version = user_balances.version + 1,
			updated_at = now()
		RETURNING user_id, balance_cents, currency, version, updated_at
	`
	var bal Balance
	if err := tx.QueryRow(ctx, upQ, req.UserID, req.NewBalanceCents).Scan(
		&bal.UserID, &bal.BalanceCents, &bal.Currency, &bal.Version, &bal.UpdatedAt); err != nil {
		return nil, nil, fmt.Errorf("balance.SetBalance upsert: %w", err)
	}

	const txQ = `
		INSERT INTO billing.balance_transactions
			(user_id, amount_cents, kind, order_id, reference, description, balance_after_cents)
		VALUES ($1, $2, 'adjust', NULL, $3, $4, $5)
		RETURNING id, user_id, amount_cents, kind, COALESCE(order_id,''), reference, description, balance_after_cents, created_at
	`
	var bt BalanceTransaction
	desc := req.Description
	if desc == "" {
		desc = "admin set balance"
	}
	if err := tx.QueryRow(ctx, txQ,
		req.UserID, delta, req.Reference, desc, bal.BalanceCents,
	).Scan(&bt.ID, &bt.UserID, &bt.AmountCents, &bt.Kind, &bt.OrderID, &bt.Reference, &bt.Description, &bt.BalanceAfterCents, &bt.CreatedAt); err != nil {
		return nil, nil, fmt.Errorf("balance.SetBalance ledger: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &bal, &bt, nil
}

func (r *pgBalanceRepo) ListTransactions(ctx context.Context, userID string, limit, offset int) ([]*BalanceTransaction, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	const countQ = `SELECT COUNT(*) FROM billing.balance_transactions WHERE user_id = $1`
	var total int
	if err := r.pool.QueryRow(ctx, countQ, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	const q = `
		SELECT id, user_id, amount_cents, kind, COALESCE(order_id,''), reference, description, balance_after_cents, created_at
		FROM billing.balance_transactions WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*BalanceTransaction
	for rows.Next() {
		var bt BalanceTransaction
		if err := rows.Scan(&bt.ID, &bt.UserID, &bt.AmountCents, &bt.Kind, &bt.OrderID, &bt.Reference, &bt.Description, &bt.BalanceAfterCents, &bt.CreatedAt); err != nil {
			continue
		}
		out = append(out, &bt)
	}
	return out, total, nil
}

// ---- mem impl ----

type memBalanceRepo struct {
	mu       sync.Mutex
	balances map[string]*Balance
	txns     map[string][]*BalanceTransaction
	nextID   int64
}

// NewMemBalanceRepo 构造内存仓库。
func NewMemBalanceRepo() BalanceRepo {
	return &memBalanceRepo{
		balances: map[string]*Balance{},
		txns:     map[string][]*BalanceTransaction{},
	}
}

func (r *memBalanceRepo) Get(_ context.Context, userID string) (*Balance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.balances[userID]; ok {
		cp := *b
		return &cp, nil
	}
	return &Balance{UserID: userID, Currency: "CNY"}, nil
}

func (r *memBalanceRepo) Credit(_ context.Context, req CreditRequest) (*Balance, *BalanceTransaction, error) {
	if req.AmountCents <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	if req.Kind == "" {
		return nil, nil, errors.New("balance: kind required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.balances[req.UserID]
	if !ok {
		b = &Balance{UserID: req.UserID, Currency: "CNY"}
		r.balances[req.UserID] = b
	}
	b.BalanceCents += req.AmountCents
	b.Version++
	b.UpdatedAt = time.Now().UTC()
	r.nextID++
	bt := &BalanceTransaction{
		ID:                r.nextID,
		UserID:            req.UserID,
		AmountCents:       req.AmountCents,
		Kind:              req.Kind,
		OrderID:           req.OrderID,
		Reference:         req.Reference,
		Description:       req.Description,
		BalanceAfterCents: b.BalanceCents,
		CreatedAt:         time.Now().UTC(),
	}
	r.txns[req.UserID] = append(r.txns[req.UserID], bt)
	cp := *b
	cpb := *bt
	return &cp, &cpb, nil
}

func (r *memBalanceRepo) Debit(_ context.Context, req DebitRequest) (*Balance, *BalanceTransaction, error) {
	if req.AmountCents <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	if req.Kind == "" {
		return nil, nil, errors.New("balance: kind required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.balances[req.UserID]
	if !ok || b.BalanceCents < req.AmountCents {
		return nil, nil, ErrInsufficientBalance
	}
	b.BalanceCents -= req.AmountCents
	b.Version++
	b.UpdatedAt = time.Now().UTC()
	r.nextID++
	bt := &BalanceTransaction{
		ID:                r.nextID,
		UserID:            req.UserID,
		AmountCents:       -req.AmountCents,
		Kind:              req.Kind,
		OrderID:           req.OrderID,
		Reference:         req.Reference,
		Description:       req.Description,
		BalanceAfterCents: b.BalanceCents,
		CreatedAt:         time.Now().UTC(),
	}
	r.txns[req.UserID] = append(r.txns[req.UserID], bt)
	cp := *b
	cpb := *bt
	return &cp, &cpb, nil
}

func (r *memBalanceRepo) SetBalance(_ context.Context, req SetRequest) (*Balance, *BalanceTransaction, error) {
	if req.NewBalanceCents < 0 {
		return nil, nil, ErrInvalidAmount
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.balances[req.UserID]
	if !ok {
		b = &Balance{UserID: req.UserID, Currency: "CNY"}
		r.balances[req.UserID] = b
	}
	delta := req.NewBalanceCents - b.BalanceCents
	b.BalanceCents = req.NewBalanceCents
	b.Version++
	b.UpdatedAt = time.Now().UTC()
	r.nextID++
	desc := req.Description
	if desc == "" {
		desc = "admin set balance"
	}
	bt := &BalanceTransaction{
		ID:                r.nextID,
		UserID:            req.UserID,
		AmountCents:       delta,
		Kind:              BalanceKindAdjust,
		Reference:         req.Reference,
		Description:       desc,
		BalanceAfterCents: b.BalanceCents,
		CreatedAt:         time.Now().UTC(),
	}
	r.txns[req.UserID] = append(r.txns[req.UserID], bt)
	cp := *b
	cpb := *bt
	return &cp, &cpb, nil
}

func (r *memBalanceRepo) ListTransactions(_ context.Context, userID string, limit, offset int) ([]*BalanceTransaction, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	all := r.txns[userID]
	total := len(all)
	out := make([]*BalanceTransaction, 0, limit)
	for i := total - 1 - offset; i >= 0 && len(out) < limit; i-- {
		cp := *all[i]
		out = append(out, &cp)
	}
	return out, total, nil
}
