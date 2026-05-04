-- +goose Up
SET search_path TO auth;

-- 把"当前套餐"上提到 users 级：
--   - 之前：每把 API Key 自带 plan_id；新建 Key 默认 'free'，即便用户已升级
--   - 现在：users.plan_id 是用户的"当前套餐"；新 Key 创建时继承该值；admin 切套餐
--     同时改 users.plan_id + 批量改活跃 keys
ALTER TABLE auth.users
    ADD COLUMN IF NOT EXISTS plan_id TEXT NOT NULL DEFAULT 'free';

COMMENT ON COLUMN auth.users.plan_id IS '用户当前套餐（free/basic/pro/enterprise）；新 API Key 继承此值。';

-- 一次性回填：把每个用户最近一把活跃 Key 的 plan_id 写回 users.plan_id；
-- 没有 Key 的用户保持默认 'free'。
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema='auth' AND table_name='api_keys' AND column_name='plan_id'
    ) THEN
        UPDATE auth.users u
           SET plan_id = COALESCE((
               SELECT plan_id FROM auth.api_keys k
               WHERE k.user_id = u.id AND k.revoked_at IS NULL
               ORDER BY k.created_at DESC LIMIT 1
           ), u.plan_id);
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
SET search_path TO auth;
ALTER TABLE auth.users DROP COLUMN IF EXISTS plan_id;
