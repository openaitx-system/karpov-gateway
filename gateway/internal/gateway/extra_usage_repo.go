package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExtraUsageSettings 是用户对"超额使用 / Extra Usage"的配置。
//
// Enabled 为 true 时：当月度配额（plan.monthly_limit）耗尽后，
// 请求继续放行，按 plan.overage_price_per_1k 计费；
// 当 MonthlyCapCents>0 且本月累计超额费用 >= cap 时仍会被拒绝。
type ExtraUsageSettings struct {
	UserID             string    `json:"userId"`
	Enabled            bool      `json:"enabled"`
	MonthlyCapCents    int64     `json:"monthlyCapCents"`
	NotifyThresholdPct int       `json:"notifyThresholdPct"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// OverageCharge 是 (user, year_month) 维度的超额累计账单。
type OverageCharge struct {
	UserID      string    `json:"userId"`
	YearMonth   string    `json:"yearMonth"`
	PlanID      string    `json:"planId"`
	Count       int64     `json:"count"`
	WeightSum   int64     `json:"weightSum"`
	AmountCents int64     `json:"amountCents"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// OverageEvent 是单次超额事件流水（用于审计/明细展示）。
type OverageEvent struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"userId"`
	Provider   string    `json:"provider"`
	Endpoint   string    `json:"endpoint"`
	PlanID     string    `json:"planId"`
	Weight     int       `json:"weight"`
	PriceCents int64     `json:"priceCents"`
	Timestamp  time.Time `json:"timestamp"`
}

// ExtraUsageRepo 抽象超额开关的读写。PG 与 In-Memory 两套实现。
type ExtraUsageRepo interface {
	GetSettings(ctx context.Context, userID string) (*ExtraUsageSettings, error)
	UpsertSettings(ctx context.Context, s *ExtraUsageSettings) error
	GetCharge(ctx context.Context, userID, yearMonth string) (*OverageCharge, error)
	AddCharge(ctx context.Context, userID, yearMonth, planID string, weight int, priceCents int64) (*OverageCharge, error)
	RecordEvent(ctx context.Context, ev *OverageEvent) error
	ListEvents(ctx context.Context, userID string, limit int) ([]*OverageEvent, error)
	ListCharges(ctx context.Context, userID string, limit int) ([]*OverageCharge, error)
}

// ---- PG impl ----

type pgExtraUsageRepo struct {
	pool *pgxpool.Pool
}

// NewPgExtraUsageRepo 构造 PG 仓库；pool=nil 时返回 mem 实现的指针不被推荐，
// 调用方应根据 PG 是否可用选择 NewMemExtraUsageRepo。
func NewPgExtraUsageRepo(pool *pgxpool.Pool) ExtraUsageRepo {
	return &pgExtraUsageRepo{pool: pool}
}

func (r *pgExtraUsageRepo) GetSettings(ctx context.Context, userID string) (*ExtraUsageSettings, error) {
	const q = `
		SELECT user_id, enabled, monthly_cap_cents, notify_threshold_pct, updated_at
		FROM billing.extra_usage_settings WHERE user_id = $1
	`
	var s ExtraUsageSettings
	err := r.pool.QueryRow(ctx, q, userID).Scan(
		&s.UserID, &s.Enabled, &s.MonthlyCapCents, &s.NotifyThresholdPct, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &ExtraUsageSettings{
				UserID: userID, Enabled: false, MonthlyCapCents: 0, NotifyThresholdPct: 80,
			}, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *pgExtraUsageRepo) UpsertSettings(ctx context.Context, s *ExtraUsageSettings) error {
	const q = `
		INSERT INTO billing.extra_usage_settings (user_id, enabled, monthly_cap_cents, notify_threshold_pct, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			monthly_cap_cents = EXCLUDED.monthly_cap_cents,
			notify_threshold_pct = EXCLUDED.notify_threshold_pct,
			updated_at = now()
	`
	_, err := r.pool.Exec(ctx, q, s.UserID, s.Enabled, s.MonthlyCapCents, s.NotifyThresholdPct)
	return err
}

func (r *pgExtraUsageRepo) GetCharge(ctx context.Context, userID, yearMonth string) (*OverageCharge, error) {
	const q = `
		SELECT user_id, year_month, plan_id, count, weight_sum, amount_cents, updated_at
		FROM billing.overage_charges WHERE user_id = $1 AND year_month = $2
	`
	var c OverageCharge
	err := r.pool.QueryRow(ctx, q, userID, yearMonth).Scan(
		&c.UserID, &c.YearMonth, &c.PlanID, &c.Count, &c.WeightSum, &c.AmountCents, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &OverageCharge{UserID: userID, YearMonth: yearMonth}, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *pgExtraUsageRepo) AddCharge(ctx context.Context, userID, yearMonth, planID string, weight int, priceCents int64) (*OverageCharge, error) {
	const q = `
		INSERT INTO billing.overage_charges (user_id, year_month, plan_id, count, weight_sum, amount_cents, updated_at)
		VALUES ($1, $2, $3, 1, $4, $5, now())
		ON CONFLICT (user_id, year_month) DO UPDATE SET
			count = overage_charges.count + 1,
			weight_sum = overage_charges.weight_sum + $4,
			amount_cents = overage_charges.amount_cents + $5,
			plan_id = COALESCE(NULLIF(EXCLUDED.plan_id, ''), overage_charges.plan_id),
			updated_at = now()
		RETURNING user_id, year_month, plan_id, count, weight_sum, amount_cents, updated_at
	`
	var c OverageCharge
	err := r.pool.QueryRow(ctx, q, userID, yearMonth, planID, weight, priceCents).Scan(
		&c.UserID, &c.YearMonth, &c.PlanID, &c.Count, &c.WeightSum, &c.AmountCents, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *pgExtraUsageRepo) RecordEvent(ctx context.Context, ev *OverageEvent) error {
	const q = `
		INSERT INTO billing.overage_events (user_id, provider, endpoint, plan_id, weight, price_cents, ts)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, now()))
	`
	var ts any
	if !ev.Timestamp.IsZero() {
		ts = ev.Timestamp
	}
	_, err := r.pool.Exec(ctx, q, ev.UserID, ev.Provider, ev.Endpoint, ev.PlanID, ev.Weight, ev.PriceCents, ts)
	return err
}

func (r *pgExtraUsageRepo) ListEvents(ctx context.Context, userID string, limit int) ([]*OverageEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, user_id, provider, endpoint, plan_id, weight, price_cents, ts
		FROM billing.overage_events WHERE user_id = $1 ORDER BY ts DESC LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OverageEvent
	for rows.Next() {
		var ev OverageEvent
		if err := rows.Scan(&ev.ID, &ev.UserID, &ev.Provider, &ev.Endpoint, &ev.PlanID, &ev.Weight, &ev.PriceCents, &ev.Timestamp); err != nil {
			continue
		}
		out = append(out, &ev)
	}
	return out, nil
}

func (r *pgExtraUsageRepo) ListCharges(ctx context.Context, userID string, limit int) ([]*OverageCharge, error) {
	if limit <= 0 || limit > 60 {
		limit = 12
	}
	const q = `
		SELECT user_id, year_month, plan_id, count, weight_sum, amount_cents, updated_at
		FROM billing.overage_charges WHERE user_id = $1 ORDER BY year_month DESC LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OverageCharge
	for rows.Next() {
		var c OverageCharge
		if err := rows.Scan(&c.UserID, &c.YearMonth, &c.PlanID, &c.Count, &c.WeightSum, &c.AmountCents, &c.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &c)
	}
	return out, nil
}

// ---- mem impl ----

type memExtraUsageRepo struct {
	mu       sync.RWMutex
	settings map[string]*ExtraUsageSettings
	charges  map[string]map[string]*OverageCharge // userID -> yearMonth -> charge
	events   map[string][]*OverageEvent
	nextID   int64
}

// NewMemExtraUsageRepo 构造内存仓库（开发/测试用）。
func NewMemExtraUsageRepo() ExtraUsageRepo {
	return &memExtraUsageRepo{
		settings: map[string]*ExtraUsageSettings{},
		charges:  map[string]map[string]*OverageCharge{},
		events:   map[string][]*OverageEvent{},
	}
}

func (r *memExtraUsageRepo) GetSettings(_ context.Context, userID string) (*ExtraUsageSettings, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.settings[userID]; ok {
		cp := *s
		return &cp, nil
	}
	return &ExtraUsageSettings{UserID: userID, Enabled: false, MonthlyCapCents: 0, NotifyThresholdPct: 80}, nil
}

func (r *memExtraUsageRepo) UpsertSettings(_ context.Context, s *ExtraUsageSettings) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	cp.UpdatedAt = time.Now().UTC()
	r.settings[s.UserID] = &cp
	return nil
}

func (r *memExtraUsageRepo) GetCharge(_ context.Context, userID, yearMonth string) (*OverageCharge, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if m, ok := r.charges[userID]; ok {
		if c, ok2 := m[yearMonth]; ok2 {
			cp := *c
			return &cp, nil
		}
	}
	return &OverageCharge{UserID: userID, YearMonth: yearMonth}, nil
}

func (r *memExtraUsageRepo) AddCharge(_ context.Context, userID, yearMonth, planID string, weight int, priceCents int64) (*OverageCharge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.charges[userID]; !ok {
		r.charges[userID] = map[string]*OverageCharge{}
	}
	c, ok := r.charges[userID][yearMonth]
	if !ok {
		c = &OverageCharge{UserID: userID, YearMonth: yearMonth, PlanID: planID}
		r.charges[userID][yearMonth] = c
	}
	if planID != "" {
		c.PlanID = planID
	}
	c.Count++
	c.WeightSum += int64(weight)
	c.AmountCents += priceCents
	c.UpdatedAt = time.Now().UTC()
	cp := *c
	return &cp, nil
}

func (r *memExtraUsageRepo) RecordEvent(_ context.Context, ev *OverageEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	cp := *ev
	cp.ID = r.nextID
	if cp.Timestamp.IsZero() {
		cp.Timestamp = time.Now().UTC()
	}
	r.events[ev.UserID] = append(r.events[ev.UserID], &cp)
	return nil
}

func (r *memExtraUsageRepo) ListEvents(_ context.Context, userID string, limit int) ([]*OverageEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	all := r.events[userID]
	out := make([]*OverageEvent, 0, len(all))
	// 倒序（最新优先）
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		cp := *all[i]
		out = append(out, &cp)
	}
	return out, nil
}

func (r *memExtraUsageRepo) ListCharges(_ context.Context, userID string, limit int) ([]*OverageCharge, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 {
		limit = 12
	}
	m := r.charges[userID]
	if len(m) == 0 {
		return nil, nil
	}
	out := make([]*OverageCharge, 0, len(m))
	for _, c := range m {
		cp := *c
		out = append(out, &cp)
	}
	// 简单按 yearMonth 倒序排序
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].YearMonth > out[i].YearMonth {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
