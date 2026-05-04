-- +goose Up
SET search_path TO billing;

-- 动态套餐表（替代硬编码）
CREATE TABLE IF NOT EXISTS billing.plans (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    price_cents     BIGINT NOT NULL DEFAULT 0,
    currency        TEXT NOT NULL DEFAULT 'CNY',
    period          TEXT NOT NULL DEFAULT 'monthly' CHECK (period IN ('monthly','yearly','lifetime')),
    qps             INT NOT NULL DEFAULT 5,
    daily_limit     BIGINT NOT NULL DEFAULT 100,
    monthly_limit   BIGINT NOT NULL DEFAULT 1000,
    soft_limit_pct  INT NOT NULL DEFAULT 80,
    pay_as_you_go   BOOLEAN NOT NULL DEFAULT FALSE,
    overage_price_per_1k BIGINT NOT NULL DEFAULT 0,
    sort_order      INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 初始化默认套餐
INSERT INTO billing.plans (id, name, description, price_cents, period, qps, daily_limit, monthly_limit, pay_as_you_go, overage_price_per_1k, sort_order)
VALUES
    ('free', '免费版', '基础功能体验', 0, 'monthly', 5, 100, 1000, FALSE, 0, 0),
    ('basic', '基础版', '个人开发者', 900, 'monthly', 20, 1000, 10000, TRUE, 100, 1),
    ('pro', '专业版', '团队与商业项目', 2900, 'monthly', 50, 5000, 100000, TRUE, 50, 2),
    ('enterprise', '企业版', '大规模生产环境', 9900, 'monthly', 200, 50000, 1000000, TRUE, 20, 3)
ON CONFLICT (id) DO NOTHING;

-- 更新触发器函数：从 plans 表动态读取限额
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.get_plan_qps(p_plan_id TEXT)
RETURNS INT AS $$
DECLARE
    v_qps INT;
BEGIN
    SELECT qps INTO v_qps FROM billing.plans WHERE id = p_plan_id AND is_active = TRUE;
    IF NOT FOUND THEN RETURN 5; END IF;
    RETURN v_qps;
END;
$$ LANGUAGE plpgsql STABLE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.get_plan_daily_limit(p_plan_id TEXT)
RETURNS BIGINT AS $$
DECLARE
    v_limit BIGINT;
BEGIN
    SELECT daily_limit INTO v_limit FROM billing.plans WHERE id = p_plan_id AND is_active = TRUE;
    IF NOT FOUND THEN RETURN 100; END IF;
    RETURN v_limit;
END;
$$ LANGUAGE plpgsql STABLE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.get_plan_monthly_limit(p_plan_id TEXT)
RETURNS BIGINT AS $$
DECLARE
    v_limit BIGINT;
BEGIN
    SELECT monthly_limit INTO v_limit FROM billing.plans WHERE id = p_plan_id AND is_active = TRUE;
    IF NOT FOUND THEN RETURN 1000; END IF;
    RETURN v_limit;
END;
$$ LANGUAGE plpgsql STABLE;
-- +goose StatementEnd

-- +goose Down
SET search_path TO billing;
DROP TABLE IF EXISTS billing.plans;
