-- +goose Up
SET search_path TO billing;

-- 用户余额（钱包）。一个用户一条记录；balance_cents 总是与 SUM(balance_transactions.amount_cents) 一致，
-- 由 Repo 层在事务内同时更新两张表来保证。version 用于乐观锁防并发竞争。
CREATE TABLE IF NOT EXISTS billing.user_balances (
    user_id        TEXT PRIMARY KEY,
    balance_cents  BIGINT NOT NULL DEFAULT 0 CHECK (balance_cents >= 0),
    currency       TEXT NOT NULL DEFAULT 'CNY',
    version        BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 余额流水（账本）。append-only，金额带符号：+ 充值 / - 扣费 / - 退款。
-- 所有对 user_balances 的修改都必须配套写一条流水（事务内）。
CREATE TABLE IF NOT EXISTS billing.balance_transactions (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             TEXT NOT NULL,
    amount_cents        BIGINT NOT NULL,    -- signed
    kind                TEXT NOT NULL CHECK (kind IN (
                            'topup',          -- 充值（+）
                            'overage',        -- 超额扣费（-）
                            'refund',         -- 退款（+）
                            'adjust',         -- 管理员调整（±）
                            'subscription'    -- 套餐订阅消费（-）
                        )),
    order_id            TEXT,                -- 关联订单 ID（充值/订阅时非空）
    reference           TEXT NOT NULL DEFAULT '', -- 自由引用（overage_event id, plan_id 等）
    description         TEXT NOT NULL DEFAULT '',
    balance_after_cents BIGINT NOT NULL,     -- 写入后的余额（便于审计）
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS balance_transactions_user_time ON billing.balance_transactions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS balance_transactions_order ON billing.balance_transactions (order_id) WHERE order_id IS NOT NULL;

-- 给订单加 purpose 列：subscription（默认）/ topup / one_off。
-- 支付回调按 purpose 分支处理：subscription→fulfillment / topup→credit balance。
ALTER TABLE billing.orders
    ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'subscription';
CREATE INDEX IF NOT EXISTS orders_user_purpose_idx ON billing.orders (user_id, purpose, created_at DESC);

-- +goose Down
SET search_path TO billing;
DROP INDEX IF EXISTS orders_user_purpose_idx;
ALTER TABLE billing.orders DROP COLUMN IF EXISTS purpose;
DROP TABLE IF EXISTS billing.balance_transactions;
DROP TABLE IF EXISTS billing.user_balances;
