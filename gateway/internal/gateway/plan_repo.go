package gateway

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DynamicPlan 是从 billing.plans 表读取的套餐。
type DynamicPlan struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	PriceCents         int64  `json:"priceCents"`
	Currency           string `json:"currency"`
	Period             string `json:"period"`
	QPS                int    `json:"qps"`
	DailyLimit         int64  `json:"dailyLimit"`
	MonthlyLimit       int64  `json:"monthlyLimit"`
	SoftLimitPct       int    `json:"softLimitPct"`
	PayAsYouGo         bool   `json:"payAsYouGo"`
	OveragePricePer1k  int64  `json:"overagePricePer_1k"`
	SortOrder          int    `json:"sortOrder"`
	IsActive           bool   `json:"isActive"`
}

// PlanRepo 从 PG billing.plans 表动态读取套餐。
type PlanRepo struct {
	pool *pgxpool.Pool
}

func NewPlanRepo(pool *pgxpool.Pool) *PlanRepo {
	return &PlanRepo{pool: pool}
}

func (r *PlanRepo) ListActive(ctx context.Context) ([]*DynamicPlan, error) {
	if r.pool == nil {
		return defaultPlans(), nil
	}
	const q = `
		SELECT id, name, COALESCE(description,''), price_cents, currency, period,
		       qps, daily_limit, monthly_limit, soft_limit_pct,
		       pay_as_you_go, overage_price_per_1k, sort_order, is_active
		FROM billing.plans WHERE is_active = TRUE ORDER BY sort_order ASC
	`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return defaultPlans(), nil
	}
	defer rows.Close()
	var out []*DynamicPlan
	for rows.Next() {
		var p DynamicPlan
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.Currency, &p.Period,
			&p.QPS, &p.DailyLimit, &p.MonthlyLimit, &p.SoftLimitPct,
			&p.PayAsYouGo, &p.OveragePricePer1k, &p.SortOrder, &p.IsActive,
		); err != nil {
			continue
		}
		out = append(out, &p)
	}
	if len(out) == 0 {
		return defaultPlans(), nil
	}
	return out, nil
}

func (r *PlanRepo) Get(ctx context.Context, id string) (*DynamicPlan, error) {
	if r.pool == nil {
		for _, p := range defaultPlans() {
			if p.ID == id {
				return p, nil
			}
		}
		return nil, fmt.Errorf("plan not found: %s", id)
	}
	const q = `
		SELECT id, name, COALESCE(description,''), price_cents, currency, period,
		       qps, daily_limit, monthly_limit, soft_limit_pct,
		       pay_as_you_go, overage_price_per_1k, sort_order, is_active
		FROM billing.plans WHERE id = $1 AND is_active = TRUE
	`
	var p DynamicPlan
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.Currency, &p.Period,
		&p.QPS, &p.DailyLimit, &p.MonthlyLimit, &p.SoftLimitPct,
		&p.PayAsYouGo, &p.OveragePricePer1k, &p.SortOrder, &p.IsActive,
	)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %s", id)
	}
	return &p, nil
}

// 管理接口：创建/更新/删除套餐
func (r *PlanRepo) Upsert(ctx context.Context, p *DynamicPlan) error {
	if r.pool == nil {
		return fmt.Errorf("plan repo: no PG connection")
	}
	const q = `
		INSERT INTO billing.plans (id, name, description, price_cents, currency, period,
		    qps, daily_limit, monthly_limit, soft_limit_pct,
		    pay_as_you_go, overage_price_per_1k, sort_order, is_active, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now())
		ON CONFLICT (id) DO UPDATE SET
		    name = EXCLUDED.name, description = EXCLUDED.description,
		    price_cents = EXCLUDED.price_cents, currency = EXCLUDED.currency,
		    period = EXCLUDED.period, qps = EXCLUDED.qps,
		    daily_limit = EXCLUDED.daily_limit, monthly_limit = EXCLUDED.monthly_limit,
		    soft_limit_pct = EXCLUDED.soft_limit_pct, pay_as_you_go = EXCLUDED.pay_as_you_go,
		    overage_price_per_1k = EXCLUDED.overage_price_per_1k, sort_order = EXCLUDED.sort_order,
		    is_active = EXCLUDED.is_active, updated_at = now()
	`
	_, err := r.pool.Exec(ctx, q,
		p.ID, p.Name, p.Description, p.PriceCents, p.Currency, p.Period,
		p.QPS, p.DailyLimit, p.MonthlyLimit, p.SoftLimitPct,
		p.PayAsYouGo, p.OveragePricePer1k, p.SortOrder, p.IsActive,
	)
	return err
}

func (r *PlanRepo) Delete(ctx context.Context, id string) error {
	if r.pool == nil {
		return fmt.Errorf("plan repo: no PG connection")
	}
	_, err := r.pool.Exec(ctx, `UPDATE billing.plans SET is_active = FALSE, updated_at = now() WHERE id = $1`, id)
	return err
}

func defaultPlans() []*DynamicPlan {
	return []*DynamicPlan{
		{ID: "free", Name: "免费版", PriceCents: 0, Currency: "CNY", Period: "monthly", QPS: 5, DailyLimit: 100, MonthlyLimit: 1000, SoftLimitPct: 80, IsActive: true, SortOrder: 0},
		{ID: "basic", Name: "基础版", PriceCents: 900, Currency: "CNY", Period: "monthly", QPS: 20, DailyLimit: 1000, MonthlyLimit: 10000, SoftLimitPct: 80, PayAsYouGo: true, OveragePricePer1k: 100, IsActive: true, SortOrder: 1},
		{ID: "pro", Name: "专业版", PriceCents: 2900, Currency: "CNY", Period: "monthly", QPS: 50, DailyLimit: 5000, MonthlyLimit: 100000, SoftLimitPct: 80, PayAsYouGo: true, OveragePricePer1k: 50, IsActive: true, SortOrder: 2},
		{ID: "enterprise", Name: "企业版", PriceCents: 9900, Currency: "CNY", Period: "monthly", QPS: 200, DailyLimit: 50000, MonthlyLimit: 1000000, SoftLimitPct: 80, PayAsYouGo: true, OveragePricePer1k: 20, IsActive: true, SortOrder: 3},
	}
}
