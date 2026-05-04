# Music Gateway Console (web)

Next.js 15 (App Router) + TypeScript strict + Tailwind v4 + shadcn/ui (Radix UI primitives) 编写的管理控制台。
配合仓库根 `gateway/`（Go grpc-gateway）做后端调用代理，提供登录、API Key 管理、个人设置与号池管理（admin 限定）。

## 1. 启动

```bash
cd web
cp .env.example .env.local      # 编辑 BACKEND_URL 等
pnpm install                    # 或 npm / yarn / bun
pnpm dev                        # http://localhost:3000
```

后端 gateway 默认监听 `http://localhost:8080`，前端通过 `/api/proxy/*` server-side 代理转发到 `${BACKEND_URL}/v1/*`。

## 2. 环境变量

| 变量 | 作用域 | 默认 | 必填 | 说明 |
|---|---|---|---|---|
| `BACKEND_URL` | server | — | ✅ | grpc-gateway REST 入口，仅服务端可见 |
| `SESSION_COOKIE_NAME` | server | `sid` |  | 与后端 SessionStore 一致 |
| `CSRF_COOKIE_NAME` | server | `csrf_token` |  | 与后端 CSRF 中间件一致 |
| `CSRF_HEADER_NAME` | server | `X-CSRF-Token` |  | double-submit header 名 |
| `TRUST_PROXY` | server | `false` |  | 部署在反代后才打开，控制 X-Forwarded-* 透传 |
| `PROXY_TIMEOUT_MS` | server | `15000` |  | 代理上游超时（ms） |
| `NEXT_PUBLIC_APP_NAME` | client | Music Gateway Console |  | 渲染用展示名 |
| `NEXT_PUBLIC_APP_URL` | client | `http://localhost:3000` |  | 邮件 callback 等绝对 URL |

`zod` 在模块加载时校验，缺失/错误立即抛错。

## 3. 安全模型

| 层 | 措施 |
|---|---|
| 传输 | 生产由反代/Edge 强制 HTTPS；CSP `upgrade-insecure-requests`（生产） |
| 响应头 | CSP / X-Frame-Options / X-Content-Type-Options / Referrer-Policy / Permissions-Policy / COOP / CORP（`next.config.ts` 全局注入） |
| 路由 | `middleware.ts` 在 edge 运行：未登录直接 302 至 /login；公开路径白名单 |
| 会话 | 服务端 cookie session（`sid`）由后端签发；前端从不持有原始 token |
| RSC 守卫 | `(dashboard)/layout.tsx` `requireUser()` 二次校验；`admin/layout.tsx` 校验角色 ∈ {admin, superadmin} |
| 后端鉴权 | gateway 自身再校验角色（AdminAuthMiddleware 双通路：X-Admin-Key OR session role）；前端 bypass 也无效 |
| CSRF | double-submit：`/api/csrf` 注入 cookie + JS 把同值塞 header；写请求在代理层与 gateway 层各校验一次 |
| 代理 SSRF 防护 | `/api/proxy/*` 强制白名单 `/v1/*`；路径段过滤 `..` `\` `/`；端口/协议无法注入 |
| Cookie 透传白名单 | 仅放行 `sid` / `csrf_token`；其余 cookie 不会进入后端 |
| 请求体 | 1MB 上限；超出 413 |
| 表单校验 | `zod` 在客户端做基础门槛；后端再做 zxcvbn / argon2id / 邮箱校验 |
| 暴力破解 | 后端 redis_rate 限制；前端 toast 提示 429 |
| Open Redirect | 登录回跳路径仅允许同站绝对路径，过滤 `//` 与 `/api/` |
| 日志关联 | middleware 注入 `x-request-id` 头供后端审计 |

## 4. 路由结构

```
/                 -> RSC redirect (login or dashboard)
/login            (auth) 登录
/register         (auth) 注册
/dashboard        (dashboard) 仪表盘
/api-keys         (dashboard) API Key 管理
/settings         (dashboard) 账户设置 + 改密
/admin/pool       (dashboard, admin only) 号池管理
/api/csrf         GET 引导 csrf_token
/api/proxy/*path  代理转发到 BACKEND_URL/v1/*
```

## 5. 目录结构

```
web/
├── middleware.ts                # 路由守卫
├── next.config.ts               # 安全响应头
├── postcss.config.mjs           # tailwind v4
├── components.json              # shadcn 配置
├── src/
│   ├── app/
│   │   ├── layout.tsx, page.tsx, globals.css, error.tsx, not-found.tsx
│   │   ├── (auth)/{login,register}/page.tsx
│   │   ├── (dashboard)/{dashboard,api-keys,settings,admin/pool}/page.tsx
│   │   └── api/{csrf,proxy/[...path]}/route.ts
│   ├── components/{ui,auth,api-keys,settings,layout,providers}/...
│   ├── lib/{utils,validators,env}.ts
│   ├── lib/api/{client,auth,proxy}.ts
│   ├── lib/auth/{rbac,session}.ts
│   └── types/api.ts
└── .env.example
```

## 6. 与 gateway 的契约

- 后端 REST 路径来自 `gateway/api/proto/v1/*.proto` 的 `google.api.http` 注解；
- 后端响应字段为 lowerCamelCase（grpc-gateway 默认）；
- `/v1/auth/me` 返回 `User { id, email, status, totpEnabled, createdAt, lastLoginAt, role }`；
- 所有写请求（POST/PUT/PATCH/DELETE）都需要 `X-CSRF-Token` 头（与 cookie 同值）。

## 7. 许可

GPL-3.0-or-later（与仓库根一致）。
