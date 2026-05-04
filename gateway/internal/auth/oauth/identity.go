// Package oauth 实现第三方 OAuth2/OIDC 登录与账号绑定.
//
// 设计要点 (与方案对齐):
//   - 凭据池 / OAuth 身份完全分离: oauth_identities 是"用户 ↔ 第三方账号"的映射,
//     绝不进入 pool credentials 表. provider 字段命名空间也独立 ("linuxdo" 不与
//     pool 的 "qqmusic"/"netease" 混淆).
//   - access/refresh token 用 store/crypto envelope (AES-256-GCM + KEK) 加密落库;
//     AAD 绑定 provider+sub 防止串号.
//   - state + PKCE 都在 cookie 里 (不落库), 15min 自动失效; cookie 用 HttpOnly +
//     SameSite=Lax + Secure(生产). 每个 flow 一个 cookie, 多 tab 互不干扰.
//   - 三种 callback 分支 (登录/绑定/拒绝合并) 由 service.HandleCallback 内部决断,
//     不依赖前端传 intent (intent 仅作 hint, 真正分支看当前会话 + 邮箱状态).
package oauth

import (
	"context"
	"errors"
	"time"
)

// Identity 是一条第三方账号绑定记录.
//
// 字段映射 migrations/auth/0008_oauth_identities.sql:
//
//	id                  → ID
//	user_id             → UserID
//	provider            → Provider          ("linuxdo" 等)
//	provider_sub        → ProviderSub       (provider 内的稳定用户 ID; linux.do 用 user.id 数字)
//	provider_login      → ProviderLogin     (linux.do 用户名)
//	provider_email      → ProviderEmail     (linux.do 邮箱; 可空)
//	provider_name       → ProviderName      (display name)
//	provider_avatar     → ProviderAvatar    (头像 URL)
//	trust_level         → TrustLevel        (linux.do trust_level 0-4)
//	scopes              → Scopes
//	access_token_enc    → AccessToken (明文; repo 层加解密)
//	refresh_token_enc   → RefreshToken (明文; repo 层加解密)
//	expires_at          → ExpiresAt   (access_token 过期时刻; 可零)
//	raw_profile         → RawProfile  (provider 返回的 userinfo 完整 JSON)
//	last_login_at       → LastLoginAt
type Identity struct {
	ID             string
	UserID         string
	Provider       string
	ProviderSub    string
	ProviderLogin  string
	ProviderEmail  string
	ProviderName   string
	ProviderAvatar string
	TrustLevel     int
	Scopes         []string
	AccessToken    string
	RefreshToken   string
	ExpiresAt      time.Time
	RawProfile     []byte // 原始 userinfo JSON (raw_profile JSONB)
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastLoginAt    time.Time
}

// IdentityRepo 是 oauth_identities 表的访问抽象.
//
// 接口语义:
//   - GetByProviderSub: 唯一查询入口 (provider+sub); 找不到返回 ErrIdentityNotFound.
//   - GetByUserAndProvider: 反向查询 "某用户在某 provider 的绑定";
//     找不到同样返回 ErrIdentityNotFound.
//   - ListByUser: 列出当前用户所有绑定 (供 settings/security 页面渲染);
//     无绑定返回 (nil, nil) 不报错.
//   - Upsert: provider+sub 已存在则更新 (token / profile / last_login);
//     不存在则插入. UserID 必填; 用 user_id+provider 唯一索引保证一个用户在
//     一个 provider 只绑一个账号 (防"绑两个 linuxdo 账号给同一 Karpov 账号").
//   - Delete: 按 user_id + provider 删 (用户不能解绑别人的账号).
type IdentityRepo interface {
	GetByProviderSub(ctx context.Context, provider, sub string) (*Identity, error)
	GetByUserAndProvider(ctx context.Context, userID, provider string) (*Identity, error)
	ListByUser(ctx context.Context, userID string) ([]*Identity, error)
	Upsert(ctx context.Context, id *Identity) error
	Delete(ctx context.Context, userID, provider string) error
}

// ErrIdentityNotFound 由 GetByProviderSub / GetByUserAndProvider 返回.
var ErrIdentityNotFound = errors.New("oauth: identity not found")

// ErrIdentityAlreadyBound 表示同一 provider+sub 已被另一个 Karpov 账号绑定.
// 由 Upsert 在 user_id 不匹配时抛出 (防止把同一 linuxdo 账号绑给两个本地账号).
var ErrIdentityAlreadyBound = errors.New("oauth: identity already bound to another user")
