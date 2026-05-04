package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeadersOptions 控制 Edge Gateway 出站响应头加固策略。
//
// 默认值（DefaultSecurityHeadersOptions）按 OWASP secure-headers 项目当前
// 推荐配置；调用方需要 relax 时显式覆盖（如 Embeddable iframe 关 X-Frame-Options）。
type SecurityHeadersOptions struct {
	// HSTS 控制 Strict-Transport-Security；默认开启 1 年 + includeSubDomains。
	// 开发环境（HTTP）应显式 Disable=true 关闭，避免浏览器记忆 https-only。
	HSTSDisable      bool
	HSTSMaxAgeSec    int    // 默认 31536000（1 年）
	HSTSIncludeSubs  bool   // 默认 true
	HSTSPreload      bool   // 默认 false（提交 hstspreload.org 后再开）
	ContentSecurity  string // 默认 "default-src 'self'"；空字符串保留默认
	ReferrerPolicy   string // 默认 "strict-origin-when-cross-origin"
	XFrameOptions    string // 默认 "DENY"；接受 SAMEORIGIN / ALLOW-FROM
	XContentTypeOpts string // 默认 "nosniff"
	PermissionsPol   string // 默认 "geolocation=(), microphone=(), camera=()"
}

// DefaultSecurityHeadersOptions 返回生产推荐配置。
func DefaultSecurityHeadersOptions() SecurityHeadersOptions {
	return SecurityHeadersOptions{
		HSTSMaxAgeSec:    31536000,
		HSTSIncludeSubs:  true,
		HSTSPreload:      false,
		ContentSecurity:  "default-src 'self'",
		ReferrerPolicy:   "strict-origin-when-cross-origin",
		XFrameOptions:    "DENY",
		XContentTypeOpts: "nosniff",
		PermissionsPol:   "geolocation=(), microphone=(), camera=()",
	}
}

// SecurityHeaders 是 gin middleware：写入推荐安全响应头。
//
// 应紧跟 Recovery() 后注册，覆盖所有 /healthz / /v1/* 路由。
// 失败响应（4xx/5xx）也带这些头，避免被中间盒劫持。
func SecurityHeaders(opts SecurityHeadersOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		if !opts.HSTSDisable && opts.HSTSMaxAgeSec > 0 {
			val := "max-age=" + itoa(opts.HSTSMaxAgeSec)
			if opts.HSTSIncludeSubs {
				val += "; includeSubDomains"
			}
			if opts.HSTSPreload {
				val += "; preload"
			}
			h.Set("Strict-Transport-Security", val)
		}
		if opts.ContentSecurity != "" {
			h.Set("Content-Security-Policy", opts.ContentSecurity)
		}
		if opts.ReferrerPolicy != "" {
			h.Set("Referrer-Policy", opts.ReferrerPolicy)
		}
		if opts.XFrameOptions != "" {
			h.Set("X-Frame-Options", opts.XFrameOptions)
		}
		if opts.XContentTypeOpts != "" {
			h.Set("X-Content-Type-Options", opts.XContentTypeOpts)
		}
		if opts.PermissionsPol != "" {
			h.Set("Permissions-Policy", opts.PermissionsPol)
		}
		c.Next()
	}
}

// CSRFOptions 控制 double-submit cookie 模式 CSRF 防护行为。
//
// 我们不依赖 gorilla/csrf，因为：
//  1. 它要求 secure cookie + 指定 secret 旋转策略，对开发环境过严；
//  2. 它和 gin 集成需要额外 wrap，两边 Context 互转易出错；
//  3. double-submit cookie 是 OWASP 推荐的标准模式，自实现 ~50 行可读性更高。
//
// 工作机制：
//   - 浏览器首次 GET → 注入 csrf_token cookie（HttpOnly=false 让 JS 读到）；
//   - 写请求（POST/PUT/DELETE/PATCH）必须把 cookie 值塞进 X-CSRF-Token 头；
//   - 中间件做常量时间比较，不一致 → 403。
//
// API Key 走 X-API-Key 头时跳过 CSRF（CORS preflight 已经禁止跨域携带）。
type CSRFOptions struct {
	CookieName string // 默认 "csrf_token"
	HeaderName string // 默认 "X-CSRF-Token"
	CookiePath string // 默认 "/"
	Secure     bool   // 生产环境务必 true
	Domain     string // 可选

	// SkipPaths 是不强制 CSRF 校验的路径前缀（健康检查 / OAuth callback 等）。
	SkipPaths []string

	// APIKeyHeader 命中时跳过校验（默认 "X-API-Key"）。
	APIKeyHeader string
}

// DefaultCSRFOptions 返回生产推荐 CSRF 配置（未开 Secure；调用方在生产显式打开）。
func DefaultCSRFOptions() CSRFOptions {
	return CSRFOptions{
		CookieName:   "csrf_token",
		HeaderName:   "X-CSRF-Token",
		CookiePath:   "/",
		APIKeyHeader: "X-API-Key",
		SkipPaths:    []string{"/healthz", "/readyz"},
	}
}

// CSRF 返回 gin middleware：double-submit cookie 模式 CSRF 防护。
//
// safe method（GET/HEAD/OPTIONS）只确保 cookie 存在；不安全 method 校验 header。
func CSRF(opts CSRFOptions) gin.HandlerFunc {
	if opts.CookieName == "" {
		opts.CookieName = "csrf_token"
	}
	if opts.HeaderName == "" {
		opts.HeaderName = "X-CSRF-Token"
	}
	if opts.CookiePath == "" {
		opts.CookiePath = "/"
	}
	if opts.APIKeyHeader == "" {
		opts.APIKeyHeader = "X-API-Key"
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		for _, skip := range opts.SkipPaths {
			if strings.HasPrefix(path, skip) {
				c.Next()
				return
			}
		}

		// API Key 持有者跳过 CSRF（CORS 默认禁跨域带头）。
		if opts.APIKeyHeader != "" && c.GetHeader(opts.APIKeyHeader) != "" {
			c.Next()
			return
		}

		// 取或生成 cookie；保证 GET 后端再写一次（rotate per session 由 Auth Service 做）。
		token, err := c.Cookie(opts.CookieName)
		if err != nil || token == "" {
			tok, gerr := newCSRFToken()
			if gerr != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			token = tok
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(opts.CookieName, token,
				int((24 * 7 * 3600)), opts.CookiePath, opts.Domain, opts.Secure, false /* HttpOnly false: JS 读 */)
		}

		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			c.Next()
			return
		}

		got := c.GetHeader(opts.HeaderName)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			Fail(c, http.StatusForbidden, CodeCSRF, "csrf token mismatch")
			return
		}
		c.Next()
	}
}

// newCSRFToken 生成 256-bit base64url token（不含 padding）。
func newCSRFToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// itoa 是无依赖整数转字符串（避免 import strconv 增加 transitive dep）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
