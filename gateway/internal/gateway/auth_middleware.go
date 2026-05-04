package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// authResolveTimeout 是 SessionMiddleware 解 session 的最大等待。
const authResolveTimeout = 1 * time.Second

// SessionResolver 抽象 session→user 查询，便于 middleware 测试。
//
// 实现见 *auth.Service.VerifySession（已满足该接口）。
type SessionResolver interface {
	VerifySession(ctx context.Context, sid string) (*auth.User, *auth.Session, error)
}

// APIKeyVerifier 验证 API Key 并返回关联用户。
//
// clientIP 由 middleware 通过 c.ClientIP() 注入；Service.VerifyAPIKey 用它对
// 配置了 IPAllow 的 key 做白名单校验。
type APIKeyVerifier interface {
	VerifyAPIKey(ctx context.Context, plain, clientIP string) (*auth.User, *auth.APIKeyRecord, error)
}

// SessionMiddlewareOptions 控制 SessionMiddleware 行为。
type SessionMiddlewareOptions struct {
	// Resolver 必填：调 VerifySession。
	Resolver SessionResolver

	// APIKeyVerifier 可选：非 nil 时支持 X-API-Key 头认证（session fallback）。
	APIKeyVerifier APIKeyVerifier

	// APIKeyHeader 默认 "X-API-Key"。
	APIKeyHeader string

	// CookieName 默认 "sid"。
	CookieName string

	// HeaderName 是 sid 的备用读取头（grpc-gateway 友好）。默认 "X-Session-Id"。
	HeaderName string

	// UserHeaderOut 是注入到 request 的 user-id 头名。默认 "X-User-Id"。
	UserHeaderOut string

	// RoleHeaderOut 是注入用户角色的头名。默认 "X-User-Role"。
	RoleHeaderOut string

	// SkipPaths 是不解析 session 的路径前缀。
	SkipPaths []string

	// Required=true 时未登录直接 401；false 仅记录"未登录"，不阻断。
	Required bool
}

// SessionMiddleware 是 gin middleware：cookie sid → user，注入 X-User-Id。
//
// 流程：
//  1. SkipPaths 命中 → 直接放行
//  2. 取 cookie/header sid；为空 → Required ? 401 : 透传
//  3. VerifySession 失败（session 过期 / 用户被删）→ Required ? 401 : 透传
//  4. 成功 → 设 c.Request.Header[X-User-Id] + c.Set("auth.user", *User)
//
// fail-open 行为：Resolver 报基础设施错误时（Redis 故障）不阻断，
// 仅在 Required=true 时把它当 401 处理（避免给攻击者用 Redis 故障当成绕过通道）。
func SessionMiddleware(opts SessionMiddlewareOptions) gin.HandlerFunc {
	if opts.CookieName == "" {
		opts.CookieName = "sid"
	}
	if opts.HeaderName == "" {
		opts.HeaderName = "X-Session-Id"
	}
	if opts.UserHeaderOut == "" {
		opts.UserHeaderOut = "X-User-Id"
	}
	if opts.RoleHeaderOut == "" {
		opts.RoleHeaderOut = "X-User-Role"
	}
	if opts.APIKeyHeader == "" {
		opts.APIKeyHeader = "X-API-Key"
	}

	injectUser := func(c *gin.Context, user *auth.User) {
		c.Request.Header.Set(opts.UserHeaderOut, user.ID)
		c.Request.Header.Set(opts.RoleHeaderOut, user.EffectiveRole())
		c.Set("auth.user", user)
		c.Set("auth.role", user.EffectiveRole())
	}

	return func(c *gin.Context) {
		// 清除客户端伪造的内部头（只能由中间件设置）
		c.Request.Header.Del(opts.UserHeaderOut)
		c.Request.Header.Del(opts.RoleHeaderOut)

		path := c.Request.URL.Path
		for _, skip := range opts.SkipPaths {
			if strings.HasPrefix(path, skip) {
				c.Next()
				return
			}
		}

		// 如果显式携带 X-API-Key，必须验证其有效性（无论是否有 session）
		if opts.APIKeyVerifier != nil {
			apiKey := c.GetHeader(opts.APIKeyHeader)
			if apiKey != "" {
				ctx, cancel := context.WithTimeout(c.Request.Context(), authResolveTimeout)
				defer cancel()
				user, keyRec, err := opts.APIKeyVerifier.VerifyAPIKey(ctx, apiKey, c.ClientIP())
				if err != nil || user == nil {
					if errors.Is(err, auth.ErrAPIKeyIPNotAllowed) {
						Fail(c, http.StatusForbidden, CodeForbidden, "client ip not in api key allowlist")
						return
					}
					Fail(c, http.StatusUnauthorized, CodeUnauthorized, "invalid api key")
					return
				}
				injectUser(c, user)
				c.Set("auth.via", "apikey")
				c.Set("auth.apikey_record", keyRec)
				c.Next()
				return
			}
		}

		// 没有 API Key 时走 Session 认证
		sid := readSessionID(c, opts.CookieName, opts.HeaderName)
		if sid != "" {
			ctx, cancel := context.WithTimeout(c.Request.Context(), authResolveTimeout)
			defer cancel()
			user, _, err := opts.Resolver.VerifySession(ctx, sid)
			if err == nil && user != nil {
				injectUser(c, user)
				c.Next()
				return
			}
		}

		// 两种都没有或都无效
		if opts.Required {
			Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
			return
		}
		c.Next()
	}
}

// RequireRoleOptions 控制 RequireRole middleware 行为。
type RequireRoleOptions struct {
	// AllowedRoles 是允许通过的角色列表（如 ["admin", "superadmin"]）。
	AllowedRoles []string
	// RoleHeader 默认 "X-User-Role"（与 SessionMiddleware.RoleHeaderOut 对齐）。
	RoleHeader string
}

// RequireRole 是 RBAC gin middleware：检查 SessionMiddleware 注入的 X-User-Role
// 是否在 AllowedRoles 中；缺角色 → 401，不在白名单 → 403。
//
// 用法：
//
//	r.GET("/v1/admin/users", RequireRole(RequireRoleOptions{
//	    AllowedRoles: []string{auth.RoleAdmin, auth.RoleSuperAdmin}}), handler)
//
// 注意：本 middleware 只看 header；它必须挂在 SessionMiddleware 之后。
// 上游 service-to-service（无 session）应该走 AdminAuthMiddleware（X-Admin-Key）。
func RequireRole(opts RequireRoleOptions) gin.HandlerFunc {
	if opts.RoleHeader == "" {
		opts.RoleHeader = "X-User-Role"
	}
	allowed := make(map[string]struct{}, len(opts.AllowedRoles))
	for _, r := range opts.AllowedRoles {
		allowed[r] = struct{}{}
	}
	return func(c *gin.Context) {
		role := c.Request.Header.Get(opts.RoleHeader)
		if role == "" {
			Fail(c, http.StatusUnauthorized, CodeUnauthorized, "no session / role")
			return
		}
		if _, ok := allowed[role]; !ok {
			Fail(c, http.StatusForbidden, CodeForbidden, "insufficient role")
			return
		}
		c.Next()
	}
}

// readSessionID 按优先级取 sid：cookie > header。
func readSessionID(c *gin.Context, cookieName, headerName string) string {
	if v, _ := c.Cookie(cookieName); v != "" {
		return v
	}
	return c.GetHeader(headerName)
}

// AdminAuthOptions 控制 AdminAuthMiddleware 行为。
type AdminAuthOptions struct {
	// PathPrefix 默认 "/v1/admin/"；只对该前缀强制鉴权。
	PathPrefix string

	// AdminTokens 是允许的 service-to-service admin token 列表（X-Admin-Key）。
	// 空切片 = 关闭这条认证路径，仅靠 session 角色（下面）。
	// 生产建议每个 admin 独立 token + 定期轮换。
	AdminTokens []string

	// HeaderName 默认 "X-Admin-Key"。
	HeaderName string

	// AcceptSessionRole=true 时（默认 true）：除了 X-Admin-Key 外，也接受
	// SessionMiddleware 已注入 X-User-Role=admin/superadmin 的请求。这样
	// 登录的 admin 用户可以直接调 /v1/admin/* 而不用维护 X-Admin-Key。
	AcceptSessionRole bool

	// RoleHeader 默认 "X-User-Role"。
	RoleHeader string
}

// AdminAuthMiddleware 是 gin middleware：保护 /v1/admin/* 路径。
//
// 两条认证通路（OR）：
//  1. service-to-service：`X-Admin-Key` header 命中 AdminTokens 列表（常量时间比较）
//  2. 已登录 admin：SessionMiddleware 注入的 `X-User-Role` ∈ {admin, superadmin}
//     （AcceptSessionRole=true 时启用；默认 true）
//
// 失败码：
//
//	401 既无 X-Admin-Key 也无 session role（未登录）
//	403 提供了凭证但都不通过
//
// 生产建议：service-to-service 路径在 mTLS 上叠加（subject 校验由
// observability.LoadServerTLSConfig 提供）；session 路径靠 session TTL 自然轮换。
func AdminAuthMiddleware(opts AdminAuthOptions) gin.HandlerFunc {
	if opts.PathPrefix == "" {
		opts.PathPrefix = "/v1/admin/"
	}
	if opts.HeaderName == "" {
		opts.HeaderName = "X-Admin-Key"
	}
	if opts.RoleHeader == "" {
		opts.RoleHeader = "X-User-Role"
	}
	// 默认接受 session role；构造方零值结构体语义保持和 v0.3 旧行为一致：
	// 没设 AcceptSessionRole 也允许 session admin 通过——这是最安全的合理默认
	// （旧 token 仍工作；session admin 也工作；不允许走 token 的部署用 -admin-token=""）。
	acceptRole := opts.AcceptSessionRole
	if !acceptRole {
		// 显式置 false 时严格走 token 路径；零值结构体调用方一般不显式设 true，
		// 所以判定改用 "AdminTokens 空 → 强制走 role 路径" 的策略。
		acceptRole = len(opts.AdminTokens) == 0
	}
	// 预编码 token 字节切片，避免每次请求都做字符串→[]byte 转换。
	tokens := make([][]byte, 0, len(opts.AdminTokens))
	for _, t := range opts.AdminTokens {
		if t != "" {
			tokens = append(tokens, []byte(t))
		}
	}

	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, opts.PathPrefix) {
			c.Next()
			return
		}

		// 通路 1：X-Admin-Key
		got := c.GetHeader(opts.HeaderName)
		if got != "" {
			if len(tokens) == 0 {
				Fail(c, http.StatusForbidden, CodeForbidden, "admin disabled")
				return
			}
			gotBytes := []byte(got)
			for _, t := range tokens {
				if subtle.ConstantTimeCompare(t, gotBytes) == 1 {
					// admin token 是部署级共享密钥，持有者拥有最高权限；
					// 写 superadmin 角色让下游 SuperadminPathMiddleware 也认可这条认证路径，
					// 同时覆盖任何已被 SessionMiddleware 注入的较低角色。
					c.Request.Header.Set(opts.RoleHeader, auth.RoleSuperAdmin)
					c.Set("auth.role", auth.RoleSuperAdmin)
					c.Set("auth.via", "admin_token")
					c.Next()
					return
				}
			}
			Fail(c, http.StatusForbidden, CodeForbidden, "admin token invalid")
			return
		}

		// 通路 2：session role（admin / superadmin）
		if acceptRole {
			role := c.Request.Header.Get(opts.RoleHeader)
			if role == "admin" || role == "superadmin" {
				c.Next()
				return
			}
			if role != "" {
				Fail(c, http.StatusForbidden, CodeForbidden, "insufficient role")
				return
			}
		}

		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "admin token or admin session required")
	}
}
