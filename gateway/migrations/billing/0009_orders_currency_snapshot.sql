-- +goose Up
-- 给 billing.orders 增加货币快照三列：
--   original_currency / original_amount_cents：转换前的源币种 + 金额（典型 plan.currency / plan price）
--   fx_rate：1 单位源币 = X 单位目标币（snapshot；此后即便管理员改汇率，本订单也保留下单瞬间的换算比例）
--
-- 三列均为 NULL，老订单无值即视为"未发生跨币换算"，业务代码遇到 NULL 就退化成
-- "amount/currency 即为原始值"。零回填 / 零数据迁移成本。

SET search_path TO billing;

ALTER TABLE billing.orders
    ADD COLUMN IF NOT EXISTS original_currency     TEXT,
    ADD COLUMN IF NOT EXISTS original_amount_cents BIGINT,
    ADD COLUMN IF NOT EXISTS fx_rate               NUMERIC(20, 8);

COMMENT ON COLUMN billing.orders.original_currency     IS 'ISO 4217 source currency before FX conversion (NULL = no conversion)';
COMMENT ON COLUMN billing.orders.original_amount_cents IS 'Source amount in cents (NULL when original_currency NULL)';
COMMENT ON COLUMN billing.orders.fx_rate               IS '1 unit of original_currency = X units of currency at order creation';

-- +goose Down
SET search_path TO billing;
ALTER TABLE billing.orders
    DROP COLUMN IF EXISTS fx_rate,
    DROP COLUMN IF EXISTS original_amount_cents,
    DROP COLUMN IF EXISTS original_currency;
