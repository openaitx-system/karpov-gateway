package gateway

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// SuperadminRule 描述一条 method+path 前缀对应的 superadmin 要求。
//
// Method 为空 ⇒ 任意方法。
// Prefix 用 strings.HasPrefix 匹配；不解释路径参数。
type SuperadminRule struct {
	Method string
	Prefix string
}

// DefaultSuperadminRules 是默认要求 superadmin 角色的高敏感路径规则。
//
// 这些操作的影响面远超普通管理（涉及账号接管 / 资金 / 计费基线 / 角色提权），
// 普通 admin 不应单独调用：
//
//   - 强制重置任意用户密码  POST /v1/admin/users/:id/password
//   - 调整任意用户余额      POST /v1/admin/users/:id/balance
//   - 强切任意用户套餐      POST /v1/admin/users/:id/plan
//   - 全局支付 keys / 货币   PUT  /v1/admin/settings/{payment,currency}
//   - 套餐 CRUD             PUT/DELETE /v1/billing/plans/:id
//   - 直接改余额账本        POST /v1/billing/balance/adjust
//
// 注意：PATCH /v1/admin/users/:id 既允许 admin 改 status，也允许 superadmin 改 role；
// 不在此处一刀切，role 改动靠 admin_users_handler 内部分支保留 (admin_users_handler.go:174)。
//
// 顺序敏感：首条命中即生效。
var DefaultSuperadminRules = []SuperadminRule{
	// /v1/admin/users/:id/password|balance|plan 三类敏感 POST
	{Method: http.MethodPost, Prefix: "/v1/admin/users/"},

	// 全局支付 / 货币配置（PUT 替换整套）
	{Method: http.MethodPut, Prefix: "/v1/admin/settings/"},

	// 套餐 CRUD（GET 不限）
	{Method: http.MethodPut, Prefix: "/v1/billing/plans/"},
	{Method: http.MethodDelete, Prefix: "/v1/billing/plans/"},

	// 余额账本直接调账
	{Method: http.MethodPost, Prefix: "/v1/billing/balance/adjust"},
}

// SuperadminPathMiddlewareOptions 控制 SuperadminPathMiddleware 行为。
type SuperadminPathMiddlewareOptions struct {
	// Rules 是 method+前缀 → 要求 superadmin 的规则表；首条命中即生效。
	// 空时使用 DefaultSuperadminRules。
	Rules []SuperadminRule

	// RoleHeader 默认 "X-User-Role"（与 SessionMiddleware.RoleHeaderOut 对齐）。
	RoleHeader string
}

// SuperadminPathMiddleware 强制 superadmin 角色访问高敏感路径。
//
// 与 AdminAuthMiddleware 的关系：AdminAuthMiddleware 把 /v1/admin/* 拦下到 admin
// 或 superadmin 即放行；此 middleware 在更窄的路径子集上把门槛提到 superadmin。
// 对 /v1/admin/* 之外的高敏感路径（/v1/billing/plans/:id, /v1/billing/balance/adjust），
// 此 middleware 是唯一的角色分级关卡。
//
// 角色来源（优先级）：
//  1. Header c.Request.Header[RoleHeader]   ← SessionMiddleware 注入，覆盖 session/API Key 两条认证路径
//  2. c.Get("auth.role")                    ← 测试 / 直接 c.Set 路径的兜底
//
// 失败语义：
//   - 未认证（无角色信息） → 401
//   - 已认证但非 superadmin → 403
//
// 不影响命中规则之外的请求；它们走原本的鉴权链。
//
// 关于 API Key：API Key 认证后 c.Get("auth.role") 是底层 user 的角色（user/admin/superadmin）。
// 一个 superadmin 用户的 API Key 会通过此 middleware；普通 admin 用户的 API Key 即使带了
// admin:* scope 也会被这里拦下——这是有意为之，避免 admin 通过 API Key 间接执行 superadmin 操作。
func SuperadminPathMiddleware(opts SuperadminPathMiddlewareOptions) gin.HandlerFunc {
	rules := opts.Rules
	if rules == nil {
		rules = DefaultSuperadminRules
	}
	if opts.RoleHeader == "" {
		opts.RoleHeader = "X-User-Role"
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		method := c.Request.Method

		matched := false
		for _, r := range rules {
			if r.Method != "" && r.Method != method {
				continue
			}
			if !strings.HasPrefix(path, r.Prefix) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			c.Next()
			return
		}

		role := c.Request.Header.Get(opts.RoleHeader)
		if role == "" {
			if v, ok := c.Get("auth.role"); ok {
				role, _ = v.(string)
			}
		}
		if role == "" {
			Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
			return
		}
		if role != auth.RoleSuperAdmin {
			Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
			return
		}
		c.Next()
	}
}
