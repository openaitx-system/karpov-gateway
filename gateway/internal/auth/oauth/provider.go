package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Provider 抽象一个 OAuth2 第三方登录提供方.
//
// 实现需满足:
//   - Name() 返回稳定 provider 名 (写入 oauth_identities.provider, 不要随便改);
//   - DisplayName() 给前端按钮渲染用 (中文友好);
//   - OAuth2Config() 返回包含 ClientID/Secret/Endpoint/Scopes 的配置;
//   - FetchProfile() 用拿到的 access_token 调 userinfo, 返回标准化 Profile.
//
// 扩展提供方时只需新增一个 Provider 实现 + 注册到 Registry, 不动 service / handler 代码.
type Provider interface {
	Name() string
	DisplayName() string
	OAuth2Config() *oauth2.Config
	// FetchProfile 用 token.AccessToken 调 provider 的 userinfo, 返回标准化资料.
	// 调用方在拿到 Profile 后再做业务校验 (active / trust_level / 邮箱白名单等).
	FetchProfile(ctx context.Context, tok *oauth2.Token) (*Profile, error)
	// Validate 让 provider 自己做"是否允许登录"的硬校验 (e.g. linux.do trust_level).
	// 不通过返回 error; 通过返回 nil. 由 service 在 callback 里调.
	Validate(p *Profile) error
}

// Profile 是 provider userinfo 标准化结果.
//
// 字段对应到 oauth_identities 表:
//
//	Sub         → provider_sub  (provider 内稳定 ID; **必填**)
//	Login       → provider_login
//	Email       → provider_email (linux.do 已验邮箱; 用于本地账号自动创建)
//	Name        → provider_name (display name)
//	Avatar      → provider_avatar URL
//	TrustLevel  → trust_level (linux.do 0-4; 其他 provider 留 0)
//	EmailVerified → provider 是否声明该邮箱已验证 (linux.do active=true 视为已验)
//	Raw         → 原始 JSON, 落到 raw_profile 字段
type Profile struct {
	Sub           string
	Login         string
	Email         string
	Name          string
	Avatar        string
	TrustLevel    int
	EmailVerified bool
	Active        bool
	Raw           []byte // 原始 userinfo JSON
}

// Registry 是 Provider 注册表; key 是 Name().
type Registry struct {
	providers map[string]Provider
	order     []string // 保留注册顺序, 让前端按钮按固定顺序渲染
}

// NewRegistry 构造空注册表.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register 注册一个 provider; 重复 Name 后注册的覆盖前者.
func (r *Registry) Register(p Provider) {
	if _, exists := r.providers[p.Name()]; !exists {
		r.order = append(r.order, p.Name())
	}
	r.providers[p.Name()] = p
}

// Get 按 Name 查找; 找不到返回 nil.
func (r *Registry) Get(name string) Provider {
	if r == nil {
		return nil
	}
	return r.providers[name]
}

// List 返回所有注册的 provider, 按注册顺序.
func (r *Registry) List() []Provider {
	if r == nil {
		return nil
	}
	out := make([]Provider, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.providers[n])
	}
	return out
}

// ---- Linux.do Provider ----

// LinuxDoConfig 是 linux.do connect 应用的配置.
//
// 字段从 .env 读 (OAUTH_LINUXDO_*); ClientID/Secret 必填, MinTrustLevel 默认 0.
type LinuxDoConfig struct {
	ClientID         string
	ClientSecret     string
	RedirectURL      string // 完整回调 URL (含 https://gateway.karpov.cn/v1/auth/oauth/linuxdo/callback)
	MinTrustLevel    int    // 截图选 1; 0 = 不限制
	RequireActive    bool   // 拒登 active=false 的 linux.do 账号 (推荐 true)
	Scopes           []string
	HTTPClient       *http.Client // nil = http.DefaultClient
	UserInfoURL      string       // 默认 https://connect.linux.do/api/user
	AuthorizeURL     string       // 默认 https://connect.linux.do/oauth2/authorize
	TokenURL         string       // 默认 https://connect.linux.do/oauth2/token
}

// LinuxDoProvider 实现 Provider.
type LinuxDoProvider struct {
	cfg LinuxDoConfig
	cli *http.Client
}

// NewLinuxDoProvider 构造 provider; ClientID 或 Secret 为空返回 error.
func NewLinuxDoProvider(cfg LinuxDoConfig) (*LinuxDoProvider, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("oauth.linuxdo: client_id and client_secret required")
	}
	if cfg.RedirectURL == "" {
		return nil, fmt.Errorf("oauth.linuxdo: redirect_url required")
	}
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = "https://connect.linux.do/oauth2/authorize"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://connect.linux.do/oauth2/token"
	}
	if cfg.UserInfoURL == "" {
		cfg.UserInfoURL = "https://connect.linux.do/api/user"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"read"}
	}
	cli := cfg.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 15 * time.Second}
	}
	return &LinuxDoProvider{cfg: cfg, cli: cli}, nil
}

// Name 实现 Provider.
func (p *LinuxDoProvider) Name() string { return "linuxdo" }

// DisplayName 实现 Provider.
func (p *LinuxDoProvider) DisplayName() string { return "Linux.do" }

// OAuth2Config 实现 Provider.
func (p *LinuxDoProvider) OAuth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: p.cfg.ClientSecret,
		RedirectURL:  p.cfg.RedirectURL,
		Scopes:       p.cfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   p.cfg.AuthorizeURL,
			TokenURL:  p.cfg.TokenURL,
			AuthStyle: oauth2.AuthStyleInHeader, // linux.do 用 Basic Auth 传 client credentials
		},
	}
}

// FetchProfile 调 https://connect.linux.do/api/user 拿用户资料.
//
// 返回 JSON 形如:
//
//	{
//	  "id": 123, "username": "foo", "name": "Foo",
//	  "email": "foo@example.com", "active": true,
//	  "trust_level": 2, "silenced": false,
//	  "avatar_template": "/user_avatar/.../avatar/{size}.png" (相对路径, 需要拼前缀)
//	}
//
// avatar 字段拼成完整 URL: 如果是 / 开头 → 加 https://connect.linux.do 前缀.
// {size} 模板替换为 96.
func (p *LinuxDoProvider) FetchProfile(ctx context.Context, tok *oauth2.Token) (*Profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth.linuxdo: build userinfo req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := p.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth.linuxdo: userinfo http: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 上限 1MB 防 OOM
	if err != nil {
		return nil, fmt.Errorf("oauth.linuxdo: read userinfo: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("oauth.linuxdo: userinfo status %d body=%s",
			resp.StatusCode, truncate(body, 200))
	}
	// linux.do 字段可能是 number 或 string; 用 json.Number 兼容.
	var raw struct {
		ID             json.Number `json:"id"`
		Username       string      `json:"username"`
		Name           string      `json:"name"`
		Email          string      `json:"email"`
		Active         bool        `json:"active"`
		TrustLevel     int         `json:"trust_level"`
		Silenced       bool        `json:"silenced"`
		AvatarTemplate string      `json:"avatar_template"`
		AvatarURL      string      `json:"avatar_url"` // 兜底
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("oauth.linuxdo: parse userinfo: %w", err)
	}
	if raw.ID == "" {
		return nil, fmt.Errorf("oauth.linuxdo: userinfo missing id")
	}
	avatar := raw.AvatarURL
	if avatar == "" && raw.AvatarTemplate != "" {
		t := strings.ReplaceAll(raw.AvatarTemplate, "{size}", "96")
		if strings.HasPrefix(t, "/") {
			avatar = "https://connect.linux.do" + t
		} else {
			avatar = t
		}
	}
	return &Profile{
		Sub:           raw.ID.String(),
		Login:         raw.Username,
		Email:         strings.TrimSpace(raw.Email),
		Name:          raw.Name,
		Avatar:        avatar,
		TrustLevel:    raw.TrustLevel,
		Active:        raw.Active,
		EmailVerified: raw.Active, // linux.do 已经在他们端要求邮箱验证才能 active
		Raw:           body,
	}, nil
}

// Validate 实现硬校验:
//   - RequireActive=true 时 Profile.Active 必须为 true (拒已禁用账号);
//   - TrustLevel < MinTrustLevel 拒登 (linux.do 等级门槛);
//   - 注: silenced 状态由 linux.do 在 active=false 时联动, 不需要单独判.
func (p *LinuxDoProvider) Validate(prof *Profile) error {
	if prof == nil {
		return fmt.Errorf("oauth.linuxdo: nil profile")
	}
	if p.cfg.RequireActive && !prof.Active {
		return fmt.Errorf("linux.do account inactive (silenced or unverified)")
	}
	if prof.TrustLevel < p.cfg.MinTrustLevel {
		return fmt.Errorf("linux.do trust_level=%d below required %d",
			prof.TrustLevel, p.cfg.MinTrustLevel)
	}
	return nil
}

// truncate 把 byte slice 截断 (日志友好).
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
