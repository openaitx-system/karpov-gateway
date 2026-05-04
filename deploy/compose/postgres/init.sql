-- ============================================================================
-- Music Gateway PostgreSQL 初始化脚本
--
-- 仅创建后端服务所需的 schema 与最低权限角色；表结构由 gateway/migrations/
-- 在应用启动时通过 golang-migrate 创建（M19 起规划）。
--
-- 执行时机：postgres 容器首次启动（PGDATA 为空）时被 docker-entrypoint.sh
-- 自动运行；后续启动跳过。
-- ============================================================================

\set ON_ERROR_STOP on

-- 关闭 NOTICE 噪音
SET client_min_messages = 'warning';

-- 五个业务 schema（与 gateway/internal/store/* migration 对齐）
CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS quota;
CREATE SCHEMA IF NOT EXISTS billing;
CREATE SCHEMA IF NOT EXISTS pool;
CREATE SCHEMA IF NOT EXISTS observability;

-- 显式授权当前用户（POSTGRES_USER）：默认 owner 已能访问；这里写出来便于改用
-- 独立角色（dba / app）时复制粘贴。
GRANT USAGE, CREATE ON SCHEMA auth, quota, billing, pool, observability TO CURRENT_USER;

-- 启用常用扩展
CREATE EXTENSION IF NOT EXISTS pgcrypto;     -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";  -- uuid_generate_v7 兼容（部分发行版需 ext）
CREATE EXTENSION IF NOT EXISTS pg_trgm;      -- 用户/订单模糊搜索

-- 元数据表：标记本脚本已执行（幂等审计）
CREATE TABLE IF NOT EXISTS public.deploy_init_log (
    id          SERIAL PRIMARY KEY,
    initialized_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    note        TEXT
);
INSERT INTO public.deploy_init_log (note)
VALUES ('docker-compose init.sql executed; schemas + extensions ready');
