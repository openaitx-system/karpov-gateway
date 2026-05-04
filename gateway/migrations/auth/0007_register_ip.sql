-- +goose Up
SET search_path TO auth;

-- 一个 IP 仅一个账号：注册时把客户端 IP 持久化到 users.register_ip。
-- 这是"注册时的快照"，与 last_login_ip 含义不同——后者每次登录都会刷新，
-- 而 register_ip 一旦写入只在管理员手工干预时变化。Service 在注册路径上
-- 会按这个字段做 COUNT(...) > 0 的去重检查。
ALTER TABLE auth.users
    ADD COLUMN IF NOT EXISTS register_ip INET;

COMMENT ON COLUMN auth.users.register_ip IS '注册时客户端 IP 快照；用于"一 IP 一账号"去重，命中白名单的 IP 不写入。';

-- 部分索引：只索引非空行。注册去重每次都跑 WHERE register_ip = $1，
-- 一个普通 INET 索引就够；但 NULL 行（白名单内、或老用户回填前）没必要进索引。
CREATE INDEX IF NOT EXISTS users_register_ip_idx
    ON users (register_ip)
    WHERE register_ip IS NOT NULL;

-- +goose Down
SET search_path TO auth;

DROP INDEX IF EXISTS users_register_ip_idx;
ALTER TABLE auth.users DROP COLUMN IF EXISTS register_ip;
