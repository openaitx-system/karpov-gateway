# Security

QQMusicApi Gateway 的安全设计与披露流程。

## 报告漏洞

请**不要**在公开 Issue 中提交安全问题。
- 邮件：security@qqmusic-gateway.local（占位，发布前替换）
- 加密：可用 keys.openpgp.org 上的 PGP 公钥（指纹见 release tag）

收到报告后 72h 内确认接收，30 天内给出修复时间表。CVE 协调披露遵循 90 天默认窗口。

## 威胁模型

| 资产 | 主要威胁 | 主要缓解 |
|---|---|---|
| 用户密码 | 撞库 / 离线爆破 / 数据库泄露 | argon2id（OWASP #1）+ Redis 限流 5/min/IP，10 失败锁 30min |
| Session 凭据 | XSS 偷 cookie / 固化 / 中间人 | HttpOnly + Secure + SameSite=Lax + `__Host-` 前缀；登录重生成 SID |
| API Key | 日志泄露 / 数据库泄露 | 仅创建时回显明文一次；DB 仅存 argon2id；前缀 8 字符明文便于审计 |
| 凭据池（QQ Music musickey 等） | 数据库泄露 | AES-256-GCM 落库；KEK 在云 KMS（生产）/ env（开发） |
| 支付回调 | 伪造 / 重放 / 篡改金额 | 五步校验（IP 白名单 → 签名 → 订单状态 → 金额 → 幂等 SETNX） |
| TOTP code | 30s 窗口内重放 | Redis SETNX 黑名单，TTL = period |
| 配额逃逸 | 并发竞态把计数器减成负值 | Redis Lua 原子 INCR + 撤回；refund 用 min(weight, current) |

## 关键设计决策

### 不使用 JWT
**原因（参见 Plan §8.2）**：
- `alg=none` 历史漏洞链
- HS↔RS 密钥混淆攻击
- 无法主动失效（必须维护 blacklist 才能 logout）

**选择**：服务端 Session（Redis JSON）+ HttpOnly Cookie。需要无状态 token 时改用 PASETO v4 local。

### argon2id 参数
当前 production：`t=2, m=64MiB, p=1`（OWASP 2024 推荐）。改参数前请：
1. 测量目标硬件 single hash 耗时（应在 100~500ms 区间）
2. 评估对峰值登录并发的影响
3. 旧 hash 需要在用户首次成功登录时透明 rehash

### IV=Key（AES-CBC for QIMEI）
仅在 QIMEI 算法的 RSA→AES 链路中复现 Tencent 客户端固定行为；**这不是通用密码学建议**。所有用户/订单数据加密都用 AES-256-GCM 随机 IV。

### 支付回调五步校验
```
1) IP 白名单（网关每家自报）
2) 签名（MD5/HMAC-SHA256，constant-time 比较）
3) 订单存在 & status=PAYING
4) 金额 == 订单金额（decimal 精确比较）
5) external_txn_id 幂等（DB UNIQUE + Redis SETNX 双层）
```

任一失败 → 不更新订单 + 落 `payment_callbacks` 全量审计 + 不返 ACK（让网关重试）。

## 依赖安全

CI 每次推送都会跑：
- `govulncheck ./...` — Go 模块已知漏洞
- `gosec -severity=high -confidence=medium ./...` — 代码层安全问题
- `golangci-lint` — 含 bodyclose / errorlint / errcheck

**绿色阈值**：所有 high severity 必须修复或显式 `//nolint:` 标注理由。

## 加固清单

### v0.1（已落地）

- [x] 密码 argon2id（auth/password.go）
- [x] API Key argon2id + 仅创建时回显明文（auth/apikey.go）
- [x] Session SID 256-bit base64url（auth/session.go）
- [x] Session 固化重生成（auth/service.go Login）
- [x] TOTP RFC 6238 + replay blocker（auth/totp.go）
- [x] 配额 Redis Lua 原子（quota/quota.go）
- [x] 订单状态机（billing/order.go）
- [x] 支付签名 constant-time（billing/payment/sign.go）
- [x] 敏感日志字段 REDACTED（observability/logger.go）

### v0.2（已落地）

- [x] CSRF double-submit cookie 中间件（`gateway.CSRF`）
- [x] mTLS helper（`observability.LoadServerTLSConfig` / `LoadClientTLSConfig`）— 多进程拆分时直接接入
- [x] AES-256-GCM 凭据 envelope 加解密 helper（`store/crypto`）+ Pool PgRepo 接入（AAD 绑定凭据 ID）
- [x] CSP / HSTS / X-Frame-Options / Referrer-Policy / Permissions-Policy 响应头（`gateway.SecurityHeaders`）
- [x] 密码强度评分（zxcvbn 同语义 0-4，Register 拒弱口令）+ 内置弱口令黑名单
- [x] QQ / 微信 QR HTTP 轮询登录路径

### v0.3（待落地）

- [ ] 凭据 KEK 接 KMS / Vault（v0.2 仅 env hex KEK）
- [ ] HIBP API k-anonymity 二次密码强度校验
- [ ] 二维码登录 MQTT 流式订阅（paho.golang/autopaho）
- [ ] 多进程拆分 + 内部 gRPC 上 mTLS
- [ ] PgRepo 集成测试（testcontainers-go）
