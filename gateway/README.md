# Music Gateway

Go 实现的多音乐服务聚合网关。**v0.1.0 — Walking-skeleton 完成**：REST → grpc-gateway → gRPC → Service → Pool → Provider 整条链路可跑通；QQ 音乐为首发 provider，netease 占位。

详见根目录计划文档：`C:\Users\YST\.claude\plans\go-concurrent-pudding.md`。

## 核心能力（v0.1）

| 子系统 | 状态 | 说明 |
|---|---|---|
| Auth Service | ✅ | argon2id 密码 / Redis Session（HttpOnly Cookie）/ TOTP / API Key |
| Music Service | ✅ | provider 路由 + 失败回退 N 次 + 领域模型适配 |
| Pool Service | ✅ | 加权随机调度 + 健康度状态机（OK/RateLimited/AuthFailed/NetworkError） |
| Quota Service | ✅ | Redis Lua 原子双层窗口 + 端点权重 + 软/硬限三态决策 |
| Billing Service | ✅ | 订单 FSM + uuidv7 + decimal 金额；易支付 + 虎皮椒签名验签 |
| QQ Music Provider | ✅ | musicu.fcg / musicw.fcg + 13 模块 + 手机登录 + 凭证刷新 |
| Netease Provider | 🟡 占位 | Capability=空，所有方法返回 `ErrNotImplemented` |
| 二维码登录（MQTT） | ⏳ v0.2 | `ErrMobileQRStreamNotImplemented` |
| 凭据 KMS 加密 / mTLS | ⏳ v0.2 | 当前 env KEK + 同进程 dial |

## 快速开始

```bash
# 1. 安装依赖（buf 走 GOPROXY=https://proxy.golang.org,direct）
make tools           # buf / sqlc / golangci-lint / migrate
make deps            # go mod download

# 2. 生成代码
make proto           # buf generate（gRPC + grpc-gateway + OpenAPI）

# 3. 启动开发依赖
docker compose -f deploy/compose/dev.yaml up -d   # PG + Redis（可选 mailhog/jaeger/prometheus）

# 4. 启动 Edge Gateway
go run ./cmd/gateway --http :8080 --grpc :9000 --redis 127.0.0.1:6379

# 5. 烟测
curl http://localhost:8080/healthz
curl -X POST http://localhost:8080/v1/auth/register \
     -H 'Content-Type: application/json' \
     -d '{"email":"a@b.c","password":"hunter22"}'
```

## 子目录速览

| 目录 | 说明 |
|---|---|
| `api/proto/v1/` | gRPC `.proto` 契约（单一真相源） |
| `cmd/` | 7 个二进制入口（gateway / auth / music / pool / quota / billing / worker） |
| `internal/gateway/` | gin + grpc-gateway 装配 + Auth/Music 适配器 + e2e 测试 |
| `internal/provider/qqmusic/` | Python QQMusicApi 的 Go 1:1 移植（13 模块 + JCE + 加密原语） |
| `internal/provider/qqmusicprovider/` | qqmusic 的 MusicProvider 接口适配（拆出避免循环依赖） |
| `internal/provider/netease/` | netease 占位 provider（M20） |
| `internal/{auth,music,pool,quota,billing}/` | 各 service 业务核心 |
| `internal/billing/payment/` | 易支付 / 虎皮椒 签名 + Provider Registry |
| `internal/observability/` | slog（REDACTED 13 项）+ 7 项 Prometheus 指标 + OTEL |
| `internal/store/` | pgx pool / Redis 客户端 / migrate / tx 工具 |
| `migrations/` | golang-migrate 脚本（按 schema 拆） |
| `deploy/` | Docker / compose / k8s |

## 测试

```bash
go test -count=1 ./...           # 全量回归（20 包）
go test -count=1 -cover ./...    # 覆盖率
```

## 文档

- 总体设计：根目录 `C:\Users\YST\.claude\plans\go-concurrent-pudding.md`
- 安全设计：`SECURITY.md`
- 版本变更：`CHANGELOG.md`
- 许可证：根仓库 GPLv3+；`internal/provider/qqmusic/algorithms/tripledes/` 单文件 GPL-3.0-only（来源 LDDC）。详见 `LICENSING.md`。
