-- +goose Up
SET search_path TO billing;

-- 先删除外键约束
ALTER TABLE billing.invoices DROP CONSTRAINT IF EXISTS invoices_order_id_fkey;
ALTER TABLE billing.transactions DROP CONSTRAINT IF EXISTS transactions_order_id_fkey;
ALTER TABLE billing.refunds DROP CONSTRAINT IF EXISTS refunds_order_id_fkey;

-- 改类型
ALTER TABLE billing.orders ALTER COLUMN id TYPE TEXT USING id::TEXT;
ALTER TABLE billing.orders ALTER COLUMN user_id TYPE TEXT USING user_id::TEXT;
ALTER TABLE billing.orders ALTER COLUMN plan_id TYPE TEXT USING plan_id::TEXT;
ALTER TABLE billing.orders ALTER COLUMN plan_id SET DEFAULT 'free';

ALTER TABLE billing.invoices ALTER COLUMN order_id TYPE TEXT USING order_id::TEXT;
ALTER TABLE billing.invoices ALTER COLUMN user_id TYPE TEXT USING user_id::TEXT;
ALTER TABLE billing.transactions ALTER COLUMN order_id TYPE TEXT USING order_id::TEXT;
ALTER TABLE billing.refunds ALTER COLUMN order_id TYPE TEXT USING order_id::TEXT;

-- 重建外键
ALTER TABLE billing.invoices ADD CONSTRAINT invoices_order_id_fkey FOREIGN KEY (order_id) REFERENCES billing.orders(id) ON DELETE RESTRICT;
ALTER TABLE billing.transactions ADD CONSTRAINT transactions_order_id_fkey FOREIGN KEY (order_id) REFERENCES billing.orders(id) ON DELETE RESTRICT;
ALTER TABLE billing.refunds ADD CONSTRAINT refunds_order_id_fkey FOREIGN KEY (order_id) REFERENCES billing.orders(id) ON DELETE RESTRICT;

-- +goose Down
SET search_path TO billing;

ALTER TABLE billing.invoices DROP CONSTRAINT IF EXISTS invoices_order_id_fkey;
ALTER TABLE billing.transactions DROP CONSTRAINT IF EXISTS transactions_order_id_fkey;
ALTER TABLE billing.refunds DROP CONSTRAINT IF EXISTS refunds_order_id_fkey;

ALTER TABLE billing.refunds ALTER COLUMN order_id TYPE UUID USING order_id::UUID;
ALTER TABLE billing.transactions ALTER COLUMN order_id TYPE UUID USING order_id::UUID;
ALTER TABLE billing.invoices ALTER COLUMN order_id TYPE UUID USING order_id::UUID;
ALTER TABLE billing.invoices ALTER COLUMN user_id TYPE UUID USING user_id::UUID;
ALTER TABLE billing.orders ALTER COLUMN plan_id TYPE UUID USING plan_id::UUID;
ALTER TABLE billing.orders ALTER COLUMN user_id TYPE UUID USING user_id::UUID;
ALTER TABLE billing.orders ALTER COLUMN id TYPE UUID USING id::UUID;

ALTER TABLE billing.invoices ADD CONSTRAINT invoices_order_id_fkey FOREIGN KEY (order_id) REFERENCES billing.orders(id) ON DELETE RESTRICT;
ALTER TABLE billing.transactions ADD CONSTRAINT transactions_order_id_fkey FOREIGN KEY (order_id) REFERENCES billing.orders(id) ON DELETE RESTRICT;
ALTER TABLE billing.refunds ADD CONSTRAINT refunds_order_id_fkey FOREIGN KEY (order_id) REFERENCES billing.orders(id) ON DELETE RESTRICT;
