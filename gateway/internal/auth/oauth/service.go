package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// CallbackOutcome 描述 callback 处理结果, 由 handler 翻译成 HTTP 跳转 / cookie 写入.
//
// Kind 枚举:
//   - "login":   登录成功 (新建账号 or 已绑定); Session/User 必填; RedirectTo = NextURL.
//   - "bound":   intent=bind 绑定成功; Session 维持原值 (caller 看到的 ctx 已带 sid);
//                User 是当前账号; RedirectTo 通常 /settings/security?bind=ok.
//   - "error":   失败; ErrCode 给前端做友好文案 (e.g. "trust_level_too_low");
//                Message 是详细原因 (放 query string, 仅供 dev/排查).
//                RedirectTo 通常前端 /oauth-error?code=...
type CallbackOutcome struct {
	Kind       string
	Session    *auth.Session
	User       *auth.User
	Identity   *Identity
	RedirectTo string
	ErrCode    string
	Message    string
}

// CallbackError 是已知的"业务上拒登"错误, 由 handler 翻成 friendly redirect.
type CallbackError struct {
	Code    string // "state_invalid" / "exchange_failed" / "trust_level_too_low" / "email_exists" / "already_bound" / "session_required"
	Message string // 详细描述 (日志用)
}

func (e *CallbackError) Error() string {
	return fmt.Sprintf("oauth callback error [%s]: %s", e.Code, e.Message)
}

// ServiceOptions 控制 OAuth Service 行为.
type ServiceOptions struct {
	Auth        *auth.Service
	Identities  IdentityRepo
	Registry    *Registry
	Codec       *FlowStateCodec
	Logger      *slog.Logger
	Clock       func() time.Time
	// AllowedNextHosts 是 NextURL host 白名单 (防 open redirect);
	// 空 = 仅允许相对路径 ("/foo"), 任何 //xxx 或 https://xxx 都拒绝.
	AllowedNextHosts []string
	// SuccessRedirectDefault 是 NextURL 缺失时的默认跳转 (登录成功); 默认 "/".
	SuccessRedirectDefault string
	// BindSuccessRedirect 是 intent=bind 成功后的默认跳转; 默认 "/settings/security?bind=ok".
	BindSuccessRedirect string
	// ErrorRedirectBase 是失败回弹页 (前端实现); 默认 "/oauth-error".
	ErrorRedirectBase string
}

// Service 是 OAuth 业务入口.
//
// 三个核心方法:
//   - StartFlow: 生成 state+PKCE → 编码到 cookie → 返回 (cookieValue, redirectURL).
//   - HandleCallback: 验 state → 换 token → 拉 profile → 决断三分支 → 返回 outcome.
//   - ListUserIdentities / Unbind: 个人设置页用.
type Service struct {
	opts ServiceOptions
}

// NewService 构造; 必填: Auth, Identities, Registry, Codec.
func NewService(opts ServiceOptions) (*Service, error) {
	if opts.Auth == nil {
		return nil, errors.New("oauth.Service: Auth required")
	}
	if opts.Identities == nil {
		return nil, errors.New("oauth.Service: Identities required")
	}
	if opts.Registry == nil {
		return nil, errors.New("oauth.Service: Registry required")
	}
	if opts.Codec == nil {
		return nil, errors.New("oauth.Service: Codec required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.SuccessRedirectDefault == "" {
		opts.SuccessRedirectDefault = "/"
	}
	if opts.BindSuccessRedirect == "" {
		opts.BindSuccessRedirect = "/settings?bind=ok"
	}
	if opts.ErrorRedirectBase == "" {
		opts.ErrorRedirectBase = "/oauth-error"
	}
	return &Service{opts: opts}, nil
}

// StartFlowOptions 是 StartFlow 的入参.
type StartFlowOptions struct {
	Provider string
	Intent   string // "login" | "bind"
	NextURL  string // 成功后跳转 (可空)
	UserID   string // intent=bind 时填 (callback 校验同一会话)
}

// StartFlowResult 是 StartFlow 的出参.
type StartFlowResult struct {
	CookieValue string
	RedirectURL string
}

// StartFlow 准备一次 OAuth 跳转.
//
// 步骤:
//  1. 校验 provider 已注册;
//  2. 生成 state(32B) + PKCE verifier(32B) → S256 challenge;
//  3. 把 (provider, state, verifier, intent, nextURL, userID) 序列化签名进 cookie;
//  4. 返回 (cookieValue, providerAuthorizeURL).
func (s *Service) StartFlow(o StartFlowOptions) (*StartFlowResult, error) {
	prov := s.opts.Registry.Get(o.Provider)
	if prov == nil {
		return nil, &CallbackError{Code: "unknown_provider", Message: o.Provider}
	}
	intent := o.Intent
	if intent == "" {
		intent = "login"
	}
	if intent != "login" && intent != "bind" {
		intent = "login"
	}
	if !s.isAllowedNext(o.NextURL) {
		o.NextURL = "" // 静默清掉, 防 open redirect
	}
	state, err := NewRandomToken(32)
	if err != nil {
		return nil, err
	}
	verifier, err := NewRandomToken(32)
	if err != nil {
		return nil, err
	}
	flow := &FlowState{
		Provider:     o.Provider,
		State:        state,
		PKCEVerifier: verifier,
		Intent:       intent,
		NextURL:      o.NextURL,
		UserID:       o.UserID,
		IssuedAt:     s.opts.Clock().Unix(),
	}
	cookieVal, err := s.opts.Codec.Encode(flow)
	if err != nil {
		return nil, err
	}
	authzURL := prov.OAuth2Config().AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", PKCEChallengeS256(verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return &StartFlowResult{CookieValue: cookieVal, RedirectURL: authzURL}, nil
}

// HandleCallbackInput 是 callback handler 喂给 service 的数据.
type HandleCallbackInput struct {
	Provider     string
	Code         string
	State        string
	CookieValue  string         // 来自 GET /callback 时附的 oauth_state_<provider> cookie
	CurrentUser  *auth.User     // 当前会话的用户; nil = 未登录
	IP, UserAgent string
}

// HandleCallback 处理 provider 回调.
//
// 状态机 (按 callback 三分支):
//
//	provider+sub 已绑定?
//	  ├ 是 → 直接登录 (kind="login"); 同时刷新 token / last_login.
//	  └ 否 → 当前是否登录?
//	         ├ 是 → 视为绑定 (intent 期望是 bind); 把当前用户与 sub 绑定 (kind="bound").
//	         └ 否 → 看 profile.email:
//	                ├ email 已存在本地账号 → 拒绝合并 (kind="error", code="email_exists");
//	                └ email 不存在 / 缺失   → 自动建账号 + 绑定 + 登录 (kind="login").
//
// 失败 (state/exchange/userinfo/validate) 全部返回 (nil, *CallbackError); handler 翻成 redirect.
func (s *Service) HandleCallback(ctx context.Context, in HandleCallbackInput) (*CallbackOutcome, error) {
	prov := s.opts.Registry.Get(in.Provider)
	if prov == nil {
		return nil, &CallbackError{Code: "unknown_provider", Message: in.Provider}
	}
	if in.CookieValue == "" {
		return nil, &CallbackError{Code: "state_missing", Message: "oauth state cookie missing"}
	}
	flow, err := s.opts.Codec.Decode(in.CookieValue)
	if err != nil {
		return nil, &CallbackError{Code: "state_invalid", Message: err.Error()}
	}
	if flow.Provider != in.Provider {
		return nil, &CallbackError{Code: "state_provider_mismatch",
			Message: fmt.Sprintf("cookie provider=%s, callback provider=%s", flow.Provider, in.Provider)}
	}
	if subtleStrCmp(flow.State, in.State) != 1 {
		return nil, &CallbackError{Code: "state_mismatch", Message: "state value mismatch"}
	}

	// ---- 换 token ----
	cfg := prov.OAuth2Config()
	exchangeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tok, err := cfg.Exchange(exchangeCtx, in.Code,
		oauth2.SetAuthURLParam("code_verifier", flow.PKCEVerifier),
	)
	if err != nil {
		return nil, &CallbackError{Code: "exchange_failed", Message: err.Error()}
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, &CallbackError{Code: "exchange_empty_token", Message: "provider returned empty access_token"}
	}

	// ---- 拉 profile ----
	profileCtx, cancel2 := context.WithTimeout(ctx, 15*time.Second)
	defer cancel2()
	profile, err := prov.FetchProfile(profileCtx, tok)
	if err != nil {
		return nil, &CallbackError{Code: "userinfo_failed", Message: err.Error()}
	}
	if err := prov.Validate(profile); err != nil {
		return nil, &CallbackError{Code: "validate_failed", Message: err.Error()}
	}

	identity := &Identity{
		Provider:       prov.Name(),
		ProviderSub:    profile.Sub,
		ProviderLogin:  profile.Login,
		ProviderEmail:  profile.Email,
		ProviderName:   profile.Name,
		ProviderAvatar: profile.Avatar,
		TrustLevel:     profile.TrustLevel,
		Scopes:         cfg.Scopes,
		AccessToken:    tok.AccessToken,
		RefreshToken:   tok.RefreshToken,
		ExpiresAt:      tok.Expiry,
		RawProfile:     profile.Raw,
		LastLoginAt:    s.opts.Clock().UTC(),
	}

	// ---- 三分支决断 ----
	existing, err := s.opts.Identities.GetByProviderSub(ctx, prov.Name(), profile.Sub)
	if err != nil && !errors.Is(err, ErrIdentityNotFound) {
		return nil, &CallbackError{Code: "identity_lookup_failed", Message: err.Error()}
	}

	// 分支 1: 该 sub 已绑某账号
	if existing != nil {
		// 如果当前是登录状态且不是同一个用户 → 拒 (防"我用我的 linuxdo 想偷偷绑到别人 Karpov 账号").
		if in.CurrentUser != nil && in.CurrentUser.ID != existing.UserID {
			return nil, &CallbackError{Code: "already_bound",
				Message: fmt.Sprintf("this %s account is bound to another local user", prov.Name())}
		}
		u, err := s.opts.Auth.GetUserByID(ctx, existing.UserID)
		if err != nil || u == nil {
			return nil, &CallbackError{Code: "linked_user_missing",
				Message: fmt.Sprintf("identity points to user %s but user not found", existing.UserID)}
		}
		if u.Status == "locked" || u.Status == "disabled" {
			return nil, &CallbackError{Code: "account_locked",
				Message: "linked local account is locked or disabled"}
		}
		identity.UserID = u.ID
		identity.ID = existing.ID
		identity.CreatedAt = existing.CreatedAt
		if err := s.opts.Identities.Upsert(ctx, identity); err != nil {
			return nil, &CallbackError{Code: "identity_upsert_failed", Message: err.Error()}
		}
		sess, err := s.opts.Auth.IssueSessionFor(ctx, u, in.IP, in.UserAgent)
		if err != nil {
			return nil, &CallbackError{Code: "session_issue_failed", Message: err.Error()}
		}
		return &CallbackOutcome{
			Kind: "login", Session: sess, User: u, Identity: identity,
			RedirectTo: s.resolveSuccessRedirect(flow.NextURL),
		}, nil
	}

	// 分支 2: 当前已登录 → 视为绑定
	if in.CurrentUser != nil {
		// 防"想绑给别人": flow cookie 里的 UserID 必须跟当前 session 一致 (intent=bind 时记录的)
		if flow.Intent == "bind" && flow.UserID != "" && flow.UserID != in.CurrentUser.ID {
			return nil, &CallbackError{Code: "session_mismatch",
				Message: "current user does not match flow initiator"}
		}
		identity.UserID = in.CurrentUser.ID
		if err := s.opts.Identities.Upsert(ctx, identity); err != nil {
			if errors.Is(err, ErrIdentityAlreadyBound) {
				return nil, &CallbackError{Code: "already_bound", Message: err.Error()}
			}
			return nil, &CallbackError{Code: "identity_upsert_failed", Message: err.Error()}
		}
		return &CallbackOutcome{
			Kind: "bound", User: in.CurrentUser, Identity: identity,
			RedirectTo: s.opts.BindSuccessRedirect,
		}, nil
	}

	// 分支 3: 未登录 + sub 未绑 → 看邮箱
	if profile.Email != "" && profile.EmailVerified {
		if existingByEmail, _ := s.opts.Auth.FindUserByEmail(ctx, profile.Email); existingByEmail != nil {
			// 邮箱已被另一个本地账号占用; 不能自动合并 (防 takeover).
			return nil, &CallbackError{Code: "email_exists",
				Message: "邮箱已被注册, 请先用邮箱密码登录后再到设置中心绑定"}
		}
	}

	// 自动建账号 + 绑定 + 登录
	newUser, err := s.opts.Auth.CreateOAuthLinkedUser(ctx, profile.Email)
	if err != nil {
		return nil, &CallbackError{Code: "create_user_failed", Message: err.Error()}
	}
	identity.UserID = newUser.ID
	if err := s.opts.Identities.Upsert(ctx, identity); err != nil {
		return nil, &CallbackError{Code: "identity_upsert_failed", Message: err.Error()}
	}
	sess, err := s.opts.Auth.IssueSessionFor(ctx, newUser, in.IP, in.UserAgent)
	if err != nil {
		return nil, &CallbackError{Code: "session_issue_failed", Message: err.Error()}
	}
	s.opts.Logger.Info("oauth: new user via SSO",
		"provider", prov.Name(), "user", newUser.ID,
		"login", profile.Login, "email", profile.Email)
	return &CallbackOutcome{
		Kind: "login", Session: sess, User: newUser, Identity: identity,
		RedirectTo: s.resolveSuccessRedirect(flow.NextURL),
	}, nil
}

// ListUserIdentities 列出当前用户绑定的全部 identities (供 settings/security).
func (s *Service) ListUserIdentities(ctx context.Context, userID string) ([]*Identity, error) {
	return s.opts.Identities.ListByUser(ctx, userID)
}

// Unbind 解绑当前用户在某 provider 的绑定.
//
// **安全要求**: caller 必须确认该用户至少还能用其它方式登录 (有密码或绑了别的 provider),
// 否则解绑后用户会被锁死. 本方法不做这层校验, 由 handler 在调用前判断.
func (s *Service) Unbind(ctx context.Context, userID, provider string) error {
	return s.opts.Identities.Delete(ctx, userID, provider)
}

// ProviderInfo 给 GET /v1/auth/oauth/providers 用.
type ProviderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// ListProviders 列出已注册 provider, 给前端按钮渲染用.
func (s *Service) ListProviders() []ProviderInfo {
	provs := s.opts.Registry.List()
	out := make([]ProviderInfo, 0, len(provs))
	for _, p := range provs {
		out = append(out, ProviderInfo{Name: p.Name(), DisplayName: p.DisplayName()})
	}
	return out
}

// resolveSuccessRedirect 把 NextURL 标准化; 不允许的 host / 协议直接退化为默认.
func (s *Service) resolveSuccessRedirect(next string) string {
	if next == "" || !s.isAllowedNext(next) {
		return s.opts.SuccessRedirectDefault
	}
	return next
}

// isAllowedNext 校验 next 是否安全 (防 open redirect):
//   - 允许相对路径 ("/foo?x=1"); 用 url.Parse 后必须 Scheme==""+Host=="".
//   - 不允许 // 开头 (protocol-relative).
//   - 显式 https://xxx 仅当 xxx 在 AllowedNextHosts 白名单时放行.
func (s *Service) isAllowedNext(next string) bool {
	if next == "" {
		return true // 空 = 用默认; 上层会 fallback
	}
	if strings.HasPrefix(next, "//") {
		return false
	}
	u, err := url.Parse(next)
	if err != nil {
		return false
	}
	if u.Scheme == "" && u.Host == "" {
		// 相对路径; 允许.
		return strings.HasPrefix(next, "/")
	}
	for _, h := range s.opts.AllowedNextHosts {
		if strings.EqualFold(u.Host, h) {
			return true
		}
	}
	return false
}

// subtleStrCmp 是常量时间字符串比较 (1=相等, 0=不等); 防 state 比较时序攻击.
func subtleStrCmp(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	if v == 0 {
		return 1
	}
	return 0
}
