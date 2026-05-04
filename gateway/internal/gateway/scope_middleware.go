package gateway

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// ScopeRule 描述一条 method+path 前缀对应的 scope 要求。
//
// Method 为空 ⇒ 任意方法（用于 GET/POST 都需要的资源类前缀）。
// Prefix 用 strings.HasPrefix 匹配；不解释路径参数。
type ScopeRule struct {
	Method string
	Prefix string
	Scope  string
}

// DefaultScopeRules 是出厂默认的 API Key scope 规则表。
//
// 顺序敏感：精确路径必须排在通用前缀之前；首条命中即生效。
//
// 关于 scope 命名（用户可见的命名空间；号池属于平台内部资源不暴露 pool:* scope）：
//   - music:read   音乐读取（搜索/详情/歌词/URL 等任何 GET）
//   - music:write  音乐写入（保留扩展位）
//   - billing:read 账单读取（订单/余额/发票）
//   - billing:write 账单写入（创建订单/订阅/充值/超额加购）
//   - quota:read   用量查询
//   - admin:read   管理员只读（用户列表 / 套餐列表 / 号池查看 / 设置查看）
//   - admin:write  管理员写入（号池增删改 / 用户管理 / 设置变更等）
//
// 通配语法（颁发给 key 的 scope 列表中可使用）：
//   - "*"          全局通配
//   - "module:*"   模块通配
//
// 关于 admin scope 与角色的关系：
//   - admin scope 只是 API Key 的"能力开关"——它声明"这把 key 允许调管理面 API"。
//   - 实际是否有权限执行还要看底层用户的 role（admin / superadmin）；
//     AdminAuthMiddleware 强制 /v1/admin/* 用户角色 ≥ admin；
//     SuperadminPathMiddleware 把高敏感路径（密码重置 / 余额调整 / 套餐 CRUD 等）
//     再次提到 superadmin。普通用户即使创建了 admin:* 的 key 也仍然过不了 role 闸。
//   - 号池路径 /v1/admin/pool/* 用 admin:read / admin:write 区分；pool:* scope 不
//     暴露给用户，号池 raw 凭据数据由后端处理。
var DefaultScopeRules = []ScopeRule{
	// ---- billing 写入 ----
	{Method: http.MethodPost, Prefix: "/v1/billing/orders", Scope: "billing:write"},
	{Method: http.MethodPost, Prefix: "/v1/billing/subscribe", Scope: "billing:write"},
	{Method: http.MethodPost, Prefix: "/v1/billing/subscription/", Scope: "billing:write"},
	{Method: http.MethodPost, Prefix: "/v1/billing/balance/topup", Scope: "billing:write"},
	{Method: http.MethodPost, Prefix: "/v1/billing/extra-usage", Scope: "billing:write"},

	// ---- billing 读取 ----
	{Method: http.MethodGet, Prefix: "/v1/billing/orders", Scope: "billing:read"},
	{Method: http.MethodGet, Prefix: "/v1/billing/balance", Scope: "billing:read"},
	{Method: http.MethodGet, Prefix: "/v1/billing/invoices", Scope: "billing:read"},
	{Method: http.MethodGet, Prefix: "/v1/billing/me/plan", Scope: "billing:read"},
	{Method: http.MethodGet, Prefix: "/v1/billing/plans", Scope: "billing:read"},
	{Method: http.MethodGet, Prefix: "/v1/billing/payment-channels", Scope: "billing:read"},

	// ---- billing 管理（套餐 CRUD + 余额账本调账：scope=admin:write，role=superadmin）----
	{Method: http.MethodPut, Prefix: "/v1/billing/plans/", Scope: "admin:write"},
	{Method: http.MethodDelete, Prefix: "/v1/billing/plans/", Scope: "admin:write"},
	{Method: http.MethodPost, Prefix: "/v1/billing/balance/adjust", Scope: "admin:write"},

	// ---- 用量查询 ----
	{Prefix: "/v1/usage/", Scope: "quota:read"},

	// ---- 音乐数据：所有 provider 路径都按 music:read 控制 ----
	{Prefix: "/v1/qqmusic/", Scope: "music:read"},
	{Prefix: "/v1/netease/", Scope: "music:read"},

	// ---- /v1/admin/* 管理面通配（pool / users / settings / login-qr）----
	// GET → admin:read；写方法 → admin:write。
	// 注意此处只控 scope；角色门槛由 AdminAuthMiddleware（≥admin）+
	// SuperadminPathMiddleware（高敏感子集要 superadmin）共同决定。
	{Method: http.MethodGet, Prefix: "/v1/admin/", Scope: "admin:read"},
	{Method: http.MethodPost, Prefix: "/v1/admin/", Scope: "admin:write"},
	{Method: http.MethodPut, Prefix: "/v1/admin/", Scope: "admin:write"},
	{Method: http.MethodPatch, Prefix: "/v1/admin/", Scope: "admin:write"},
	{Method: http.MethodDelete, Prefix: "/v1/admin/", Scope: "admin:write"},

	// /v1/auth/* 是账号自服务，不强制 scope（API Key 也允许查 me 等）
	// /v1/billing/callback/* 是支付回调，无 auth
	// /v1/docs/* 是 OpenAPI，公开
}

// ScopeMiddlewareOptions 控制 ScopeMiddleware 行为。
type ScopeMiddlewareOptions struct {
	// Rules 是 method+前缀 → required scope 的规则表；首条命中即生效。
	Rules []ScopeRule
	// SkipPaths 中的前缀整体跳过 scope 校验（auth/* / docs/* 等公开/自服务路径）。
	SkipPaths []string
}

// ScopeMiddleware 强制 API Key 的 scope 限制。
//
// 仅当请求是通过 API Key 认证时（auth.via=apikey）才生效；session 用户、admin token、
// 公开路径都直接放行。语义：
//
//   - keyRec.Scopes 为空     ⇒ 视为通配，任何路径都放行（兼容旧 key + 用户故意不限）
//   - keyRec.Scopes 非空     ⇒ 仅当 rules 表里命中且 keyRec 拥有该 scope 时放行；
//                              规则表里没声明的路径（如 /v1/auth/me）也放行（自服务接口）
//
// 即"声明了 scope 表示只允许调声明范围内的接口"，但只对*已被规则覆盖*的接口强制；
// 这样不破坏 OpenAPI / 用户自服务路径的现状。
func ScopeMiddleware(opts ScopeMiddlewareOptions) gin.HandlerFunc {
	rules := opts.Rules
	if rules == nil {
		rules = DefaultScopeRules
	}
	skip := append([]string{}, opts.SkipPaths...)
	if len(skip) == 0 {
		skip = []string{
			"/v1/auth/", "/v1/docs", "/v1/billing/callback/",
			"/v1/config/",
			"/healthz", "/readyz",
		}
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		for _, p := range skip {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}

		via, _ := c.Get("auth.via")
		if via != "apikey" {
			c.Next()
			return
		}
		recAny, ok := c.Get("auth.apikey_record")
		if !ok {
			c.Next()
			return
		}
		keyRec, ok := recAny.(*auth.APIKeyRecord)
		if !ok || keyRec == nil {
			c.Next()
			return
		}
		// 通配：用户没限定 scope 的 key 不受表约束
		if len(keyRec.Scopes) == 0 {
			c.Next()
			return
		}

		method := c.Request.Method
		for _, r := range rules {
			if r.Method != "" && r.Method != method {
				continue
			}
			if !strings.HasPrefix(path, r.Prefix) {
				continue
			}
			// 命中规则；检查 key 是否拥有该 scope
			if !keyRec.HasScope(r.Scope) {
				Fail(c, http.StatusForbidden, CodeForbidden,
					"api key missing required scope: "+r.Scope)
				return
			}
			break // 首条命中即停
		}
		c.Next()
	}
}
