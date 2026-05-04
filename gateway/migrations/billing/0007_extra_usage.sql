-- +goose Up
SET search_path TO billing;

-- 用户级"超额使用 / Extra Usage"开关与上限。
-- enabled = TRUE 时，月度配额耗尽后请求继续放行，按 plan.overage_price_per_1k 计费。
-- monthly_cap_cents = 0 表示不设上限；> 0 表示当本月超额费用累计达到此值后强制拒绝。
CREATE TABLE IF NOT EXISTS billing.extra_usage_settings (
    user_id              TEXT PRIMARY KEY,
    enabled              BOOLEAN NOT NULL DEFAULT FALSE,
    monthly_cap_cents    BIGINT NOT NULL DEFAULT 0 CHECK (monthly_cap_cents >= 0),
    notify_threshold_pct INT NOT NULL DEFAULT 80 CHECK (notify_threshold_pct BETWEEN 0 AND 100),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 月度超额累计：用于 cap 判定与账单展示。中间件每记录一次超额都会 UPSERT 进来。
CREATE TABLE IF NOT EXISTS billing.overage_charges (
    user_id      TEXT NOT NULL,
    year_month   CHAR(7) NOT NULL, -- "2026-05"
    plan_id      TEXT NOT NULL DEFAULT '',
    count        BIGINT NOT NULL DEFAULT 0,
    weight_sum   BIGINT NOT NULL DEFAULT 0,
    amount_cents BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, year_month)
);

-- 单次超额事件流水（用于审计/对账，可异步聚合到 overage_charges）。
CREATE TABLE IF NOT EXISTS billing.overage_events (
    id           BIGSERIAL PRIMARY KEY,
    user_id      TEXT NOT NULL,
    provider     TEXT NOT NULL DEFAULT '',
    endpoint     TEXT NOT NULL DEFAULT '',
    plan_id      TEXT NOT NULL DEFAULT '',
    weight       INT NOT NULL DEFAULT 1,
    price_cents  BIGINT NOT NULL DEFAULT 0,
    ts           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS overage_events_user_ts ON billing.overage_events (user_id, ts DESC);
-- 月度范围查询直接复用 (user_id, ts DESC) 索引；避免 to_char(timestamptz) 非 IMMUTABLE 报错。

-- +goose Down
SET search_path TO billing;
DROP TABLE IF EXISTS billing.overage_events;
DROP TABLE IF EXISTS billing.overage_charges;
DROP TABLE IF EXISTS billing.extra_usage_settings;
