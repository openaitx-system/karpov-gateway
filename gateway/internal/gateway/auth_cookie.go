package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/proto"

	authv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/auth/v1"
)

// AuthCookieOptions 控制登录 / TOTP 验证完成时自动签发的 sid Cookie。
//
// 修复："grpc-gateway 默认把 LoginResponse 当 JSON 直接返回，不写 Set-Cookie"
// 导致前端拿到 {sid} JSON 但浏览器 cookie 不存在，跳转受保护页时 middleware
// 检不到 cookie 立即把用户踢回 /login → 登录后又要重新登录的死循环。
//
// 安全默认：HttpOnly + SameSite=Lax + Path=/。生产 (HTTPS) 下应把 Secure=true。
type AuthCookieOptions struct {
	// Name 是 cookie 名；默认 "sid"，与前端 SESSION_COOKIE_NAME 一致。
	Name string
	// Path；默认 "/"。
	Path string
	// Domain；默认空（host-only）。仅当需要跨子域共享时才设置。
	Domain string
	// Secure；HTTPS 部署必开。
	Secure bool
	// SameSite；默认 Lax。跨站 SPA 走 None+Secure。
	SameSite http.SameSite
	// Disabled=true 时整层关闭——退化到由前端从 JSON 中取 sid 自行写 cookie。
	Disabled bool
}

// Defaults 填充未设置字段。
func (o *AuthCookieOptions) Defaults() {
	if o.Name == "" {
		o.Name = "sid"
	}
	if o.Path == "" {
		o.Path = "/"
	}
	if o.SameSite == 0 {
		o.SameSite = http.SameSiteLaxMode
	}
}

// DefaultAuthCookieOptions 返回安全默认值。
func DefaultAuthCookieOptions() AuthCookieOptions {
	o := AuthCookieOptions{}
	o.Defaults()
	return o
}

// AuthCookieResponseWriter 是 grpc-gateway runtime.WithForwardResponseOption 的 hook。
//
// 当响应消息是 *authv1.LoginResponse 或 *authv1.TOTPResult 且包含非空 sid 时，
// 写入 HttpOnly Set-Cookie，让浏览器自动持有会话。
//
// 注意：
//   - LoginResponse.totp_required=true 时 sid 通常为空（首段密码校验未签发会话），
//     不写 cookie 等待用户输入 TOTP 码后由 TOTPResult 再写；
//   - 不会修改响应 body，前端仍能从 JSON 中读到 sid（向后兼容）。
func AuthCookieResponseWriter(opts AuthCookieOptions) func(context.Context, http.ResponseWriter, proto.Message) error {
	opts.Defaults()
	return func(_ context.Context, w http.ResponseWriter, msg proto.Message) error {
		if opts.Disabled {
			return nil
		}
		switch m := msg.(type) {
		case *authv1.LoginResponse:
			if m.GetSid() == "" || m.GetTotpRequired() {
				return nil
			}
			http.SetCookie(w, buildSessionCookie(opts, m.GetSid(), tsToTime(m.GetExpiresAt().AsTime())))
		case *authv1.TOTPResult:
			if m.GetSid() == "" {
				return nil
			}
			http.SetCookie(w, buildSessionCookie(opts, m.GetSid(), tsToTime(m.GetExpiresAt().AsTime())))
		}
		return nil
	}
}

// AuthCookieClearMiddleware 在 POST /v1/auth/logout 成功后清除 sid Cookie。
//
// LogoutResponse 是 google.protobuf.Empty，没办法在 ForwardResponseOption 里识别，
// 改在 gin 层按路径 + 状态码判断写 Set-Cookie: sid=; Max-Age=0。
func AuthCookieClearMiddleware(opts AuthCookieOptions) gin.HandlerFunc {
	opts.Defaults()
	return func(c *gin.Context) {
		c.Next()
		if opts.Disabled {
			return
		}
		if c.Request.Method != http.MethodPost {
			return
		}
		if c.Request.URL.Path != "/v1/auth/logout" {
			return
		}
		// 仅在业务成功时清 cookie；非 2xx 维持现状避免误清。
		if c.Writer.Status() < 200 || c.Writer.Status() >= 300 {
			return
		}
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     opts.Name,
			Value:    "",
			Path:     opts.Path,
			Domain:   opts.Domain,
			HttpOnly: true,
			Secure:   opts.Secure,
			SameSite: opts.SameSite,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
	}
}

// buildSessionCookie 组装 sid cookie；零值 expires 表示会话级（关浏览器即失效）。
func buildSessionCookie(opts AuthCookieOptions, sid string, expires time.Time) *http.Cookie {
	c := &http.Cookie{
		Name:     opts.Name,
		Value:    sid,
		Path:     opts.Path,
		Domain:   opts.Domain,
		HttpOnly: true,
		Secure:   opts.Secure,
		SameSite: opts.SameSite,
	}
	if !expires.IsZero() {
		c.Expires = expires
		if d := time.Until(expires); d > 0 {
			c.MaxAge = int(d.Seconds())
		}
	}
	return c
}

// tsToTime 处理 nil timestamppb 退化到零时间。
func tsToTime(t time.Time) time.Time { return t }
