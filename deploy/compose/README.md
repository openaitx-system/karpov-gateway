# Music Gateway 开发依赖编排

`docker compose` 仅负责跑 **PostgreSQL + pgAdmin + Redis**。
**gateway 后端**与 **web 前端**在开发阶段都直接在 host 上跑，热更与调试更顺手。

## 1. 启动依赖

```bash
cd deploy/compose
cp .env.example .env
# 必改 POSTGRES_PASSWORD / PGADMIN_DEFAULT_PASSWORD / REDIS_PASSWORD
$EDITOR .env

docker compose up -d
docker compose ps
```

预期容器：

| 容器 | 监听 | 用途 |
|---|---|---|
| `mgw-postgres` | `127.0.0.1:5432` | 业务数据（auth/quota/billing/pool） |
| `mgw-pgadmin` | `127.0.0.1:5050` | PostgreSQL 可视化管理（pgAdmin 4） |
| `mgw-redis` | `127.0.0.1:6379` | session / quota / pool lease |

## 2. 访问 pgAdmin

打开 <http://localhost:5050>，使用 `.env` 里设置的 `PGADMIN_DEFAULT_EMAIL` / `PGADMIN_DEFAULT_PASSWORD` 登录。
默认 UI 语言已配置为**简体中文**（`PGADMIN_CONFIG_DEFAULT_LANGUAGE='zh'`）。
镜像版本：`dpage/pgadmin4:9.14`（最新稳定版，自动锁定避免 latest 漂移）。

登录后左侧已预注册了 `Music Gateway (mgw-postgres)` 服务器；首次连接弹窗输入 `POSTGRES_PASSWORD` 即可（pgAdmin 出于安全不允许在 `servers.json` 内硬编码密码，可以选择"保存密码"勾选记住）。

> 服务器之间走 compose 内部网络（host=`postgres`, port=`5432`）；不要在 pgAdmin 里写 `127.0.0.1`，否则会指向 pgAdmin 容器自身。

> 如果你之前已经启动过 pgAdmin（volume 内已存在用户偏好），`DEFAULT_LANGUAGE` 不会回写到老用户。两种处理：
> - 在 pgAdmin Web UI：**File → Preferences → Miscellaneous → User Language → 中文 (Chinese)**；
> - 或彻底重置：`docker compose down pgadmin && docker volume rm mgw-pgadmin-data && docker compose up -d pgadmin`。

## 3. 启动后端 gateway（host）

```bash
cd ../../gateway
go run ./cmd/qqmusic-gateway \
  -redis 127.0.0.1:6379 \
  -redis-password "$REDIS_PASSWORD" \
  -admin-token "$(openssl rand -hex 32)" \
  -bootstrap-email admin@example.com
```

> `-redis-password` 也可省略，runner 会从 `$REDIS_PASSWORD` 兜底读取。

首次启动会在 stderr 一次性打印 superadmin 明文密码——立即抄到密码管理器，进 web 后通过 `/v1/auth/password/change` 改为自有口令。

## 4. 启动 web 前端（host）

```bash
cd ../web
cp .env.example .env.local       # 默认 BACKEND_URL=http://localhost:8080
pnpm install --ignore-workspace  # 首次
pnpm dev                          # http://localhost:3000
```

## 5. 安全配置

| 项 | 措施 |
|---|---|
| PostgreSQL 认证 | `--auth-host=scram-sha-256` |
| PostgreSQL 端口 | 仅绑定 `127.0.0.1` |
| pgAdmin 模式 | `SERVER_MODE=False`（desktop / 单用户）+ master password |
| pgAdmin 端口 | 仅绑定 `127.0.0.1`；CONFIG_LOGIN_BANNER 显式标记访问限制 |
| Redis 密码 | `requirepass` 强制 |
| Redis 危险命令 | `FLUSHDB / FLUSHALL / CONFIG / DEBUG` rename 为空字符串禁用 |
| Redis 端口 | 仅绑定 `127.0.0.1` |
| 网络 | user-defined bridge `mgw-net`，服务间 DNS |
| 数据卷 | named volume，`down -v` 才会清空 |
| `.env` | `.gitignore` 排除；从不入库 |
| `no-new-privileges` | 三个容器都设置 |

## 6. 常用命令

```bash
# 查看日志
docker compose logs -f postgres
docker compose logs -f pgadmin
docker compose logs -f redis

# 进 postgres CLI
docker compose exec postgres psql -U mgw -d mgw

# 进 redis CLI（带密码）
docker compose exec redis redis-cli -a "$REDIS_PASSWORD" --no-auth-warning

# 重启依赖
docker compose restart

# 清理（保留数据卷）
docker compose down

# 完全重置（含数据卷，慎用）
docker compose down -v
```

## 7. 生产部署

本编排仅面向开发。生产部署应：

- gateway / web 各自打镜像（v0.4 规划 release pipeline）；
- pgAdmin 不要直接对外，前置 SSO 反代；
- PG 启用 SSL + 周期备份至对象存储；
- Redis 前置 stunnel/Envoy 终结 TLS；
- 反代终结 HTTPS + HSTS preload；
- 设置 `gateway -bootstrap-disable=true`，改用 IaC provisioning 创建管理员。
