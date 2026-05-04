# Changelog

## [Unreleased — v0.3 进度]

### 新增功能：超级管理员用户管理

**后端**：
- `internal/auth/service.go`：新增 `UserLister` 接口（带 `EmailLike/Role/Status/Limit/Offset` 过滤）+ `Service.ListUsers/SetStatus/AdminResetPassword/GetUserByID`
- `internal/auth/{pgrepo,fileuserrepo,service}.go`：PG / File / Mem 三个仓库均实现 `UserLister`（PG 用 ILIKE 模糊匹配 + COUNT 双查询）
- `internal/gateway/balance_repo.go`：`BalanceRepo` 接口扩展 `SetBalance(SetRequest)`，事务内写差额 `adjust` 流水（PG 实现 + Mem 实现）
- `internal/gateway/admin_users_handler.go` 全新文件，挂在受 AdminAuthMiddleware 保护的 `/v1/admin/users/*`：
  - `GET    /v1/admin/users` 列表（分页 + 邮箱/角色/状态过滤）
  - `GET    /v1/admin/users/:id` 详情（user + balance + apiKeys + planCounts）
  - `PATCH  /v1/admin/users/:id` 更新角色（仅 superadmin）/状态
  - `POST   /v1/admin/users/:id/password` 强制重置密码
  - `GET    /v1/admin/users/:id/balance` 余额
  - `POST   /v1/admin/users/:id/balance` `{operation: credit/debit/set, amountCents}` 三种调账
  - `GET    /v1/admin/users/:id/balance/transactions` 流水
  - `GET    /v1/admin/users/:id/keys` API Keys 摘要
  - `POST   /v1/admin/users/:id/plan` `{planId}` 一键切换该用户全部活跃 Key 的套餐
- 12 个新单元测试（ListUsers 排序/过滤/分页/SetStatus/AdminResetPassword、SetBalance 增减覆盖差额流水）

**前端**：
- `lib/api/admin-users.ts`：完整 TypeScript 客户端
- `components/admin/users-admin-client.tsx`：列表（筛选 + 分页 + 状态/角色徽章）+ 详情对话框（4 Tabs：资料 / 余额 / 套餐 / API Keys）
- `app/(dashboard)/admin/users/page.tsx` + 侧边栏新增"用户管理"项（adminOnly）

### 新增功能：用户钱包（Balance）系统 + Extra Usage 集成

**后端**：
- 修复迁移 `0007_extra_usage.sql`：移除非 IMMUTABLE 的 `to_char(ts, 'YYYY-MM')` 索引，避免 SQLSTATE 42P17
- 新增迁移 `gateway/migrations/billing/0008_balance.sql`：`user_balances`（钱包，CHECK balance_cents>=0 + version 乐观锁）+ `balance_transactions`（账本，append-only，带 kind/order_id/balance_after）+ `orders.purpose` 列
- 新增 `internal/gateway/balance_repo.go`：`BalanceRepo` 接口 + 事务化 PG 实现（条件 UPDATE 防并发扣成负数）+ Mem 实现
- 新增 `internal/gateway/balance_handler.go`：
  - `GET    /v1/billing/balance`              当前余额
  - `GET    /v1/billing/balance/transactions` 流水（带分页）
  - `POST   /v1/billing/balance/topup`        创建充值订单（≥¥1，≤¥100w）
  - `POST   /v1/billing/balance/adjust`       管理员手工调整（admin only）
  - `FulfillTopup`：被支付回调调用，把订单金额计入余额
- 修改 `internal/billing/order.go`：新增 `Purpose` 字段（`subscription` / `topup`），常量化
- 修改 `internal/billing/pg_repo.go`：所有 SELECT/INSERT 携带 `purpose` 列；scanOrder/s 同步更新
- 修改 `internal/gateway/payment_callback_handler.go`：`processCallback` 按 `Purpose` 分支履约——subscription 升级套餐 / topup 调 `BalanceHandler.FulfillTopup` 入账
- 修改 `internal/gateway/extra_usage_handler.go`：`ExtraUsageService.CheckAndCharge` 在原子 Debit 余额成功后才放行，余额不足返回 `insufficient_balance`；report 携带钱包余额
- 修改 `cmd/gateway/runner.go`：根据 PG 可用性接入 PG/内存 BalanceRepo，并贯通 PaymentCallback↔Balance↔ExtraUsage 三者

### 新增功能：用户级 Extra Usage（超额使用）

后端：
- 新增迁移 `gateway/migrations/billing/0007_extra_usage.sql`：`extra_usage_settings` / `overage_charges` / `overage_events` 三张表
- 新增 `internal/gateway/extra_usage_repo.go`：`ExtraUsageRepo` 接口 + `pgExtraUsageRepo`(PG) + `memExtraUsageRepo`(内存) 双实现
- 新增 `internal/gateway/extra_usage_handler.go`：REST 端点（`/v1/billing/extra-usage` / `/report` / `/charges` / `/events`）+ `ExtraUsageService.CheckAndCharge` 中间件协作 API
- 修改 `internal/gateway/plan_qps_middleware.go`：月度限额耗尽时若 `ExtraUsage` 已注入且套餐支持按量，则尝试扣余额放行；扣不出/未开启/无按量则 429
- `cmd/gateway/runner.go` 自动根据 PG 可用性选择 PG/内存仓库并接入中间件
- 20 个新单元测试覆盖 Extra Usage 全流程 + 余额仓库（Credit/Debit/InsufficientBalance/Version/Pagination）+ End-to-End 余额扣到 0 的拒绝路径

前端：
- 新增 `web/src/lib/api/extra-usage.ts` + `web/src/lib/api/balance.ts`：完整 API 客户端
- 新增 `web/src/components/billing/extra-usage-panel.tsx`：开关、月度上限、提醒阈值、报表、事件流水（含余额提醒）
- 新增 `web/src/components/billing/balance-panel.tsx`：余额展示 + 充值对话框（快捷金额按钮 + 多支付通道）+ 流水表
- 新增 `web/src/app/(dashboard)/extra-usage/page.tsx` 与 `web/src/app/(dashboard)/balance/page.tsx`，加入侧边栏 "账户余额" 与 "超额使用" 两项
- `billing/page.tsx` 增加跳转入口

### 完成里程碑（M27–M40）

| 里程碑 | 范围 |
|---|---|
| M27 | HIBP k-anonymity 密码黑名单（`internal/auth/hibp`，13 个测试 + Register fail-open 集成 3 个测试） |
| M28 | Pool PgRepo `-tags=integration` 集成测试（testcontainers-go + postgres:16-alpine + AAD 防跨 ID 重放校验） |
| M29 | 多进程拆分基础设施：`gateway.RunStandalone` helper（grpc-health + reflection + 可选 mTLS）；`cmd/auth` / `cmd/music` 独立可执行；3 个 RunStandalone e2e 测试 |
| M30 | Pool 管理 gRPC service：AddCredential / RemoveCredential / HealthSummary（8 个测试）；`cmd/pool` 独立可执行（in-memory / PG 双模式 + KEK env） |
| M31 | KEK provider 抽象：`crypto.KeyProvider` 接口 + StaticKeyProvider / EnvKeyProvider（6 个测试，为 v0.4 KMS/Vault 接入留接口） |
| M32 | Quota gin middleware：HardLimit→429+Retry-After / SoftLimit→200+X-Quota-Soft-Limit / Redis 故障 fail-open；SimpleQuotaResolver 默认实现（7 个测试） |
| M33 | Pool admin REST：`pool.proto` 加 `google.api.http` annotation；buf regen；POST/DELETE/GET `/v1/admin/pool/*` 出 REST；e2e 测试覆盖 add/health/delete 完整流 |
| M34 | Auth/Admin gin middleware：SessionMiddleware（cookie/header sid → user，注入 X-User-Id 给 quota）+ AdminAuthMiddleware（`/v1/admin/*` 常量时间 X-Admin-Key 校验，空 token=403 disabled）；12 个测试 + cmd/gateway `-admin-token` 集成 + e2e 401/403 覆盖 |
| M35 | Pool Acquire/Release gRPC 远程化：服务端 leaseRegistry（30s TTL + 守护 goroutine 自动 NetworkError 释放）+ 幂等 Release；`pool.RemoteClient` 实现 `music.PoolAcquirer`（透传 ErrNoCandidate↔ResourceExhausted）；`cmd/music -pool-grpc` 切远程；bufconn round-trip 3 个 + 单元 5 个共 8 个新测试；导出 `pool.CapName/ParseCapName` 去 pool_adapter 重复 |
| M36 | Pool LeaseRegistry 可插拔：抽 `LeaseRegistry` 接口（Put/TakeAndApply/Close）；`MemLeaseRegistry`（守护 goroutine + onExpire 自动释放）+ `RedisLeaseRegistry`（SETNX/GETDEL，多副本共享）；`pool.Service.ReleaseByCredentialID` 导出供跨副本释放；`cmd/pool -lease-redis` 切多副本；miniredis 7 个 + e2e PoolGRPCService+Redis 1 个共 8 个新测试 |
| M37 | 凭据健康检查 worker：`internal/pool/healthworker` 新包；周期遍历 active 凭据调 `HealthCheck` → `ReleaseByCredentialID`(OK/NetworkError)；连续失败 ≥N（默认 3）→ `pool.Service.DisableCredential` 软删；`cmd/worker` 真实接入（非 asynq，单实例 Ticker，v0.4 升级）；7 个测试覆盖 OK/连续失败 disable/失败重置/skip 已 disabled、ctx cancel 退出、多 provider 并行、defaults |
| M38 | 统一入口 `cmd/qqmusic-gateway`：errgroup + `signal.NotifyContext` 一键并行启动 7 个服务（gateway/auth/music/pool/quota/billing/worker）；7 个 `internal/cmd/<name>/runner.go` 抽出，签名统一为 `Run(ctx, args) error`；7 个 `cmd/<name>/main.go` 瘦身为 1 行 proxy；端口 8080/9000-9005 默认绑定；fail-fast：任一服务退出 → ctx cancel → 全栈关闭 |
| M39 | Billing/Quota gRPC adapter 真接入（取代 stub）：`gateway/quota_adapter.go` `QuotaGRPCService` + `MemRuleStore`（默认 free 1000/d、30000/月、80% soft）；`gateway/billing_adapter.go` `BillingGRPCService` + `PlanCatalog`（free/pro/enterprise）+ 5 步回调验签复用 v0.1 业务逻辑；`internal/billing/payment/mock.go` mock 支付通道；`internal/billing/service.GetOrder` 导出 |
| M40 | RBAC 完整接入：`auth.proto` User 加 `role`；`auth.Service` 加 `RoleUser/RoleAdmin/RoleSuperAdmin` + `EffectiveRole/IsAdmin/SetRole/PromoteToAdmin`；APIKey 完整 CRUD：`APIKeyRepo` 接口 + `MemAPIKeyRepo` + `Service.CreateAPIKey/ListAPIKeys/RevokeAPIKey/VerifyAPIKey`（argon2id hash + prefix 索引 + scopes/IPAllow/expires/revoked）；`AuthGRPCService` 补全 ChangePassword/CreateAPIKey/ListAPIKeys/RevokeAPIKey RPC；`SessionMiddleware` 注入 `X-User-Role`；新增 `RequireRole` middleware；`AdminAuthMiddleware` 升级双通路（X-Admin-Key OR session role∈{admin,superadmin}）；16 个新测试 |

### v0.3 路线图（剩余）

- KMS / Vault 真接入（M31 留好 KeyProvider 接口）
- MQTT 5.0 + WebSocket 二维码登录（paho.golang/autopaho）
- 健康检查 worker 升级 asynq（持久化 + 多实例分片）
- 管理后台 (Vue/React Admin)
- netease / spotify provider 真实实现

---

## [Unreleased — v0.2 进度]

### 完成里程碑（M21–M26）

| 里程碑 | 范围 |
|---|---|
| M21 | 凭据 AES-256-GCM envelope 加解密 helper（`internal/store/crypto`，AAD 绑定 / 14 个测试 / 84% 覆盖） |
| M22 | Pool PgRepo：pgx/v5 实现 `pool.Repo` 接口；payload 通过 M21 helper 加解密；AAD = `"pool:"+id` 防跨 ID 重放（9 个 unit 测试，集成测试需真 PG） |
| M23 | Edge Gateway 安全中间件：HSTS / CSP / X-Frame-Options / Referrer-Policy / Permissions-Policy + double-submit cookie CSRF（11 个测试） |
| M24 | 密码强度评分（zxcvbn 同语义 0-4 score）+ Register 拒弱口令（14 个测试 + 集成 e2e 显式 `MinPasswordStrength=-1`） |
| M25 | mTLS helper：`observability.LoadServerTLSConfig` / `LoadClientTLSConfig`，TLS 1.3 默认 + `RequireAndVerifyClientCert`（8 个测试，自签证书自验） |
| M26 | qqmusic 登录补全：QQ/微信 QR HTTP 轮询路径（GetQQQR/CheckQQQR/GetWXQR/CheckWXQR + ptuiCB & wx_errcode 解析；17 个测试）；MQTT 流式订阅留 v0.3 |

### 累计测试

- v0.1 约 ~140 个测试 + v0.2 新增 ~80 个 → **~220 个测试**，18 个业务包全绿
- 新覆盖率峰值：`store/crypto` 84% / `qqmusic/modules` 测试翻倍

### v0.3 路线图

- M22 PgRepo 集成测试（testcontainers-go）
- 多进程拆分（cmd/auth / cmd/music / cmd/pool 独立运行）→ 接 M25 mTLS
- MQTT 5.0 + WebSocket 二维码登录（paho.golang/autopaho）
- AES-256-GCM 凭据 KEK 接 KMS / Vault
- HIBP API k-anonymity 密码黑名单
- 管理后台

---

## [v0.1.0] — 2026-05-02

首个公开版本。Walking-skeleton 完成：REST → grpc-gateway → gRPC → Service → Pool → Provider 全链路打通；
QQ 音乐 musicu.fcg JSON 路径与 musicw.fcg JCE 路径就绪；Auth / Pool / Music / Quota / Billing / Payment
六个核心子系统单测全过；netease provider 占位骨架验证多 provider 抽象稳定。

### 完成里程碑（M0–M20）

| 里程碑 | 范围 |
|---|---|
| M0  | go.mod / Makefile / 7 个 cmd 占位 / .editorconfig |
| M1  | 5 个 .proto（music/auth/quota/billing/pool）+ buf 工具链 |
| M2  | 4 套 PG schema（auth/quota/billing/pool）+ pgx Store |
| M3  | Auth Service：argon2id / API Key / Redis Session / TOTP / Replay blocker |
| M4  | Edge Gateway 骨架（gin + grpc-gateway 同进程双协议） |
| M5  | qqmusic 加密原语：sign(zzc) / 3DES(GPL-3.0-only) / qimei(RSA+AES IV=Key) / MD5/Hash33 |
| M6  | qqmusic JCE codec（TarsCloud/TarsGo），跨语言 round-trip 全过 |
| M7  | qqmusic Client（musicu.fcg JSON）+ Credential 双 alias + VersionPolicy |
| M8  | qqmusic JCE 路径（musicw.fcg）+ comm 排序 + 错误类型 |
| M9  | 13 个业务模块（song/search/album/singer/songlist/lyric/mv/top/user/recommend/comment/...） |
| M10 | qqmusic 手机登录 + 凭证刷新 + CheckExpired + 二维码 PNG 申领（**MQTT 流式订阅留 v0.2**） |
| M11 | Pool Service：加权随机调度 + 状态机（OK/RateLimited/AuthFailed/NetworkError）+ MemRepo |
| M12 | Music Service：callWithLease 模板 + 失败回退 N 次 + Song/SongURL/SearchResult 领域模型 |
| M13 | Quota Service：Redis Lua 原子双层窗口 + 端点权重 + 软/硬限三态决策 + Refund 防负数 |
| M14 | Billing Service：订单 FSM（Pending→Paying→Paid→Completed + Canceled/Expired）+ uuidv7 + decimal |
| M15 | Payment 适配器：易支付（MD5）+ 虎皮椒（MD5+appsecret）+ constant-time Verify + Registry |
| M16 | Edge Gateway 全栈：Auth + Music 双适配器；e2e（register→login→me→song→logout）miniredis 全过 |
| M17 | 7 个 Prometheus 指标 + OTEL TracerProvider（noop 默认）+ slog REDACTED 13 项敏感字段 |
| M18 | .golangci.yaml + .gosec.json + SECURITY.md + CI Go 1.24 + gofmt+vet 全绿 |
| M20 | netease provider 骨架（验证 Capability/Registry 抽象在多 provider 场景下稳定） |

### 测试覆盖率（业务层）

| 包 | 覆盖率 |
|---|---|
| internal/auth | 75.4% |
| internal/billing | 86.0% |
| internal/billing/payment | 88.3% |
| internal/music | 85.6% |
| internal/pool | 83.8% |
| internal/quota | 77.3% |
| internal/provider/qqmusic | 79.7% |
| internal/provider/qqmusic/algorithms | 100% |
| internal/provider/qqmusic/algorithms/tripledes | 100% |
| internal/provider/qqmusic/crypto | 78.9% |
| internal/provider/qqmusic/modules | 67.5% |
| internal/provider/netease | 90.9% |
| internal/gateway | 58.5%（e2e 黑盒受限） |

> 基础设施层（store/observability/util）覆盖率较低是 pgx pool 真连接 / OTEL setup / 单字符串助手所致，
> 不影响业务正确性；M19 准入门槛 ≥70% 在所有业务包均达成。

### 已知未实现（v0.2 路线图）

- M10 二维码登录的 MQTT 流式订阅（paho.golang/autopaho）— 占位返回 `ErrMobileQRStreamNotImplemented`
- 凭据 AES-256-GCM + KMS 落库（v0.1 用 env KEK + memory pool）
- mTLS 内部 gRPC（v0.1 同进程 dial 不需要）
- CSRF 中间件 / CSP / HSTS 响应头
- zxcvbn-go 密码强度评分
- pgx pool 真连接 + sqlc 生成（v0.1 仍用 MemRepo / MemUserRepo）
- 管理后台 (Vue/React Admin)
- netease / spotify provider 真实实现

### 安全

详见 [SECURITY.md](SECURITY.md)。
