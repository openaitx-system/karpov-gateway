// Package gateway: OAuth REST handlers.
//
// 路由表 (全部挂在 /v1/auth/oauth):
//
//	GET    /v1/auth/oauth/providers              公开; 列出已配置 provider
//	GET    /v1/auth/oauth/{provider}/start       公开; 302 → provider authorize URL
//	GET    /v1/auth/oauth/{provider}/callback    公开; provider 回调 → 302 前端
//	GET    /v1/auth/oauth/identities             需登录; 当前用户绑定列表
//	DELETE /v1/auth/oauth/identities/{provider}  需登录; 解绑
//
// CSRF / Auth 跳过策略 (在 runner.go SkipPaths 配):
//   - providers / start / callback 都不要 session, 不走 CSRF (callback 是 provider 跳回的 GET, 没法带 token).
//   - identities 必须 session + CSRF (修改/查询自己的资源).
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	"github.com/MiChongs/QQMusicApi/gateway/internal/auth/oauth"
)

// OAuthHandler 装配 OAuth REST 端点.
//
// 依赖:
//   - svc: oauth.Service (提供 StartFlow / HandleCallback / List / Unbind);
//   - auth: auth.Service (Unbind 前判定"还能登录吗"; 也用于 callback 拿 current user).
//   - cookie: 复用 AuthCookieOptions (Secure / SameSite 一致)
type OAuthHandler struct {
	svc        *oauth.Service
	auth       *auth.Service
	cookieOpts AuthCookieOptions
	// 默认 ErrorRedirectBase, 失败 redirect 用; service 里也存了一份, 这里复制以便单独配.
	errorBase string
}

// NewOAuthHandler 构造.
func NewOAuthHandler(svc *oauth.Service, authSvc *auth.Service, cookieOpts AuthCookieOptions, errorBase string) *OAuthHandler {
	cookieOpts.Defaults()
	if errorBase == "" {
		errorBase = "/oauth-error"
	}
	return &OAuthHandler{svc: svc, auth: authSvc, cookieOpts: cookieOpts, errorBase: errorBase}
}

// Mount 注册到 gin.
func (h *OAuthHandler) Mount(e *gin.Engine) {
	g := e.Group("/v1/auth/oauth")
	g.GET("/providers", h.listProviders)
	g.GET("/:provider/start", h.start)
	g.GET("/:provider/callback", h.callback)
	g.GET("/identities", h.listIdentities)
	g.DELETE("/identities/:provider", h.unbind)
}

// listProviders: GET /v1/auth/oauth/providers
//
// 公开端点; 返回前端按钮渲染数据.
//
// 响应: { code, message, data: { providers: [{name, displayName}] } }
func (h *OAuthHandler) listProviders(c *gin.Context) {
	OK(c, gin.H{"providers": h.svc.ListProviders()})
}

// start: GET /v1/auth/oauth/{provider}/start?intent=login|bind&next=/path
//
// 行为:
//  1. 校验 provider 存在;
//  2. intent=bind 时必须有 session (取 X-User-Id);
//  3. svc.StartFlow → 拿 cookieValue + redirectURL;
//  4. Set-Cookie: oauth_state_<provider>=...; HttpOnly Secure SameSite=Lax MaxAge=15min;
//  5. 302 → provider authorize URL.
func (h *OAuthHandler) start(c *gin.Context) {
	provider := c.Param("provider")
	intent := c.DefaultQuery("intent", "login")
	next := c.Query("next")

	var userID string
	if intent == "bind" {
		// SessionMiddleware 把 /v1/auth/oauth/ 列入 SkipPaths, 所以 X-User-Id
		// 不会被注入. 这里直接读 sid cookie 自己解析.
		if u := h.currentUserFromSid(c); u != nil {
			userID = u.ID
		}
		if userID == "" {
			Fail(c, http.StatusUnauthorized, CodeUnauthorized, "bind requires login")
			return
		}
	}

	res, err := h.svc.StartFlow(oauth.StartFlowOptions{
		Provider: provider, Intent: intent, NextURL: next, UserID: userID,
	})
	if err != nil {
		var cbErr *oauth.CallbackError
		if errors.As(err, &cbErr) {
			Fail(c, http.StatusBadRequest, CodeBadRequest, cbErr.Error())
			return
		}
		Fail(c, http.StatusInternalServerError, CodeInternal, "oauth start failed")
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     oauth.CookieName(provider),
		Value:    res.CookieValue,
		Path:     "/v1/auth/oauth/",
		HttpOnly: true,
		Secure:   h.cookieOpts.Secure,
		SameSite: http.SameSiteLaxMode, // OAuth 跳转回来必须放 Lax/None+Secure
		MaxAge:   int(oauth.FlowStateMaxAge.Seconds()),
	})
	c.Redirect(http.StatusFound, res.RedirectURL)
}

// callback: GET /v1/auth/oauth/{provider}/callback?code=...&state=...
//
// 行为:
//  1. 读 cookie + provider param + code/state query;
//  2. svc.HandleCallback → outcome;
//  3. 三种 outcome:
//     - kind=login  : 写 sid cookie + 302 → outcome.RedirectTo
//     - kind=bound  : 不写 sid (沿用旧会话) + 302 → outcome.RedirectTo
//     - error       : 302 → /oauth-error?code=xxx&message=<safe>
//  4. 不论结果都清理 oauth_state_<provider> cookie.
func (h *OAuthHandler) callback(c *gin.Context) {
	provider := c.Param("provider")
	code := c.Query("code")
	state := c.Query("state")
	cookieName := oauth.CookieName(provider)

	// 取 cookie 后立即清掉 (防重放; 即便回调失败也清).
	cookie, _ := c.Request.Cookie(cookieName)
	h.clearStateCookie(c, provider)

	// provider 端报错时 (用户拒绝授权) 会带 ?error=access_denied.
	if perr := c.Query("error"); perr != "" {
		h.redirectError(c, perr, c.Query("error_description"))
		return
	}

	if code == "" || state == "" || cookie == nil || cookie.Value == "" {
		h.redirectError(c, "state_missing", "callback missing code/state/cookie")
		return
	}

	// 当前是否已登录? (intent=bind 路径需要)
	// SkipPaths 命中后中间件不注入 X-User-Id, 自己读 sid cookie 解析.
	currentUser := h.currentUserFromSid(c)

	outcome, err := h.svc.HandleCallback(c.Request.Context(), oauth.HandleCallbackInput{
		Provider:    provider,
		Code:        code,
		State:       state,
		CookieValue: cookie.Value,
		CurrentUser: currentUser,
		IP:          clientIP(c),
		UserAgent:   c.Request.UserAgent(),
	})
	if err != nil {
		var cbErr *oauth.CallbackError
		if errors.As(err, &cbErr) {
			h.redirectError(c, cbErr.Code, cbErr.Message)
			return
		}
		h.redirectError(c, "internal", err.Error())
		return
	}

	switch outcome.Kind {
	case "login":
		// 写 sid cookie 让浏览器持有会话, 与 LoginResponse 路径完全一致.
		http.SetCookie(c.Writer,
			buildSessionCookie(h.cookieOpts, outcome.Session.SID, outcome.Session.ExpiresAt))
		c.Redirect(http.StatusFound, outcome.RedirectTo)
	case "bound":
		c.Redirect(http.StatusFound, outcome.RedirectTo)
	default:
		h.redirectError(c, "unknown_outcome", outcome.Kind)
	}
}

// listIdentities: GET /v1/auth/oauth/identities
//
// 需登录; 返回当前用户绑定的全部 identities (脱敏: 不返 token).
func (h *OAuthHandler) listIdentities(c *gin.Context) {
	user := h.currentUserFromSid(c)
	if user == nil {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	uid := user.ID
	list, err := h.svc.ListUserIdentities(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "list identities failed")
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, id := range list {
		out = append(out, gin.H{
			"provider":       id.Provider,
			"providerSub":    id.ProviderSub,
			"providerLogin":  id.ProviderLogin,
			"providerEmail":  id.ProviderEmail,
			"providerName":   id.ProviderName,
			"providerAvatar": id.ProviderAvatar,
			"trustLevel":     id.TrustLevel,
			"createdAt":      id.CreatedAt.UTC().Format(time.RFC3339),
			"lastLoginAt":    nullTime(id.LastLoginAt),
		})
	}
	OK(c, gin.H{"identities": out})
}

// unbind: DELETE /v1/auth/oauth/identities/{provider}
//
// 需登录 + CSRF; 校验"解绑后还能登录" (有密码 OR 还有别的 identity).
// 否则拒绝 (CodeBadRequest), 提示用户先设密码.
func (h *OAuthHandler) unbind(c *gin.Context) {
	user := h.currentUserFromSid(c)
	if user == nil {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	uid := user.ID
	provider := c.Param("provider")
	if provider == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider required")
		return
	}
	// "解绑后还能登录吗?" — 简化策略:
	//   - 该用户必须 (a) 有密码登录 (password_hash 非占位) 或 (b) 还绑了其它 provider.
	//   - 当前 OAuth-only 账号 (没本地密码) + 只绑了一个 → 拒.
	canRemove, reason, err := h.canSafelyUnbind(c.Request.Context(), uid, provider)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "check unbind safety failed")
		return
	}
	if !canRemove {
		Fail(c, http.StatusBadRequest, CodeBadRequest, reason)
		return
	}
	if err := h.svc.Unbind(c.Request.Context(), uid, provider); err != nil {
		if errors.Is(err, oauth.ErrIdentityNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "identity not found")
			return
		}
		Fail(c, http.StatusInternalServerError, CodeInternal, "unbind failed")
		return
	}
	OK(c, gin.H{"unbound": provider})
}

// canSafelyUnbind 判定解绑后用户是否还能登录.
//
// 当前简化规则: 只要还有任何其它 identity, 或者本地账号有 password_hash 即可解绑.
// 完美方案需要标记 password_hash 是 OAuth 占位还是真实密码 — 后续优化.
func (h *OAuthHandler) canSafelyUnbind(ctx context.Context, userID, provider string) (bool, string, error) {
	identities, err := h.svc.ListUserIdentities(ctx, userID)
	if err != nil {
		return false, "", err
	}
	others := 0
	for _, id := range identities {
		if id.Provider != provider {
			others++
		}
	}
	if others > 0 {
		return true, "", nil
	}
	// 没其他 identity → 必须有可登录密码. 这里粗略判断: 用户能用密码登录 =
	// users.password_hash 不是 OAuth 占位. 没法直接区分 (占位 hash 也是 argon2),
	// 但若 user 还能记起原密码则不会用 OAuth 绑定 → 简化: 拒解绑.
	// 推荐用户在解绑前主动"设置密码" (后续上线密码重置流程后, 设密码 = 重置密码).
	return false, "解绑后将无法登录, 请先设置一个密码或绑定其它登录方式", nil
}

// redirectError 把 callback 失败统一翻成 302 → /oauth-error?code=...&message=...
func (h *OAuthHandler) redirectError(c *gin.Context, code, message string) {
	q := url.Values{}
	q.Set("code", code)
	if message != "" && len(message) < 200 {
		q.Set("message", message)
	}
	c.Redirect(http.StatusFound, h.errorBase+"?"+q.Encode())
}

// currentUserFromSid 从 sid cookie 直接解析当前 user.
//
// 为什么不读 X-User-Id: SessionMiddleware 把 /v1/auth/oauth/ 列入 SkipPaths
// (登录 / callback / providers 不需要会话), 所以这条路径不会注入 X-User-Id.
// 而 OAuth 自己的几个端点 (start?intent=bind / identities / unbind) 又确实需要
// "知道当前用户是谁". 这里手动复用 auth.Service.VerifySession 解决.
//
// 找不到 / 失效返回 nil; 不返 error (callback 流程上层用 nil 判断分支).
func (h *OAuthHandler) currentUserFromSid(c *gin.Context) *auth.User {
	sid := readSessionIDForOAuth(c, h.cookieOpts.Name)
	if sid == "" {
		return nil
	}
	user, _, err := h.auth.VerifySession(c.Request.Context(), sid)
	if err != nil || user == nil {
		return nil
	}
	return user
}

// readSessionIDForOAuth 从 cookie 或 Authorization: Bearer 拿 sid; 与中间件一致.
func readSessionIDForOAuth(c *gin.Context, cookieName string) string {
	if cookieName == "" {
		cookieName = "sid"
	}
	if ck, err := c.Request.Cookie(cookieName); err == nil && ck.Value != "" {
		return ck.Value
	}
	auth := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if len(auth) > len(prefix) && auth[:len(prefix)] == prefix {
		return auth[len(prefix):]
	}
	return ""
}

// clearStateCookie 清掉 oauth_state_<provider> cookie.
func (h *OAuthHandler) clearStateCookie(c *gin.Context, provider string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     oauth.CookieName(provider),
		Value:    "",
		Path:     "/v1/auth/oauth/",
		HttpOnly: true,
		Secure:   h.cookieOpts.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// clientIP 从 X-Forwarded-For / RemoteAddr 取客户端 IP. 反代后 gateway 信任 XFF.
func clientIP(c *gin.Context) string {
	if ip := c.GetHeader("X-Forwarded-For"); ip != "" {
		// 多级代理时取第一个 (最远端的客户端).
		if idx := strings.IndexByte(ip, ','); idx > 0 {
			return strings.TrimSpace(ip[:idx])
		}
		return strings.TrimSpace(ip)
	}
	return c.ClientIP()
}

// nullTime 把零值时间转成 nil (JSON null), 否则正常 RFC3339.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
