package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ActivationToken 是注册后下发的"账号激活令牌"。
//
// 设计：base64url(32 random bytes) ≈ 43 字符，URL-safe；前端把 token 拼到
// /activate?token=xxx 的链接里，邮件正文展示该链接（也展示纯文本 token 作 fallback）。
//
// 与"6 位验证码"的区别：
//   - 验证码 → 注册前证明邮箱，输入到表单里
//   - 激活令牌 → 注册后证明邮箱，点击邮件链接打开网页 → 由前端 POST 给 /v1/auth/email/verify
type ActivationToken struct {
	Token     string
	UserID    string
	ExpiresAt time.Time
	IssuedAt  time.Time
	UsedAt    time.Time // 零值 = 未使用
}

// ActivationTokenStore 抽象激活令牌的持久化。
//
// 同一用户允许多张未消费的 token 共存（用户多次"重发激活邮件"会拿到新 token；
// 旧 token 仍可用直到自然过期）。在 ActivateAccount 验证通过时仅消费命中那张。
type ActivationTokenStore interface {
	// Put 写入新令牌。
	Put(ctx context.Context, t *ActivationToken) error
	// GetByToken 查找有效令牌：不存在、已过期、已使用一律返回 ErrActivationTokenInvalid 系列。
	GetByToken(ctx context.Context, token string) (*ActivationToken, error)
	// MarkUsed 标记令牌已消费；同一令牌再 GetByToken 返回 ErrActivationTokenUsed。
	MarkUsed(ctx context.Context, token string, at time.Time) error
	// DeleteByUser 主动清空某用户所有未使用令牌（admin 重置 / 已激活的清扫）；返回删除条数。
	DeleteByUser(ctx context.Context, userID string) (int, error)
}

// 已知错误。
var (
	// ErrActivationTokenInvalid 令牌不存在或被篡改。
	ErrActivationTokenInvalid = errors.New("auth: activation token invalid")
	// ErrActivationTokenExpired 令牌过 TTL。前端应展示"链接已过期"+ 重发按钮。
	ErrActivationTokenExpired = errors.New("auth: activation token expired")
	// ErrActivationTokenUsed 同一令牌已消费过；前端展示"已激活，请直接登录"。
	ErrActivationTokenUsed = errors.New("auth: activation token already used")
	// ErrAccountAlreadyActive 当前账号状态已经是 active；无需再激活。
	ErrAccountAlreadyActive = errors.New("auth: account already active")
	// ErrAccountNotActivated 账号尚未激活；登录路径返回此错误。
	ErrAccountNotActivated = errors.New("auth: account not activated")
)

// NewActivationToken 生成 256-bit 随机 token（base64url 无填充）。
//
// 比 SID 多 11 字符（同样熵）；base64url 不需要 URL 编码，可直接拼到 query string。
func NewActivationToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth.NewActivationToken: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// ---- PG 实现（使用 auth.email_verifications 表）----

// PgActivationTokenStore 用 PG 表 auth.email_verifications 落地。
//
// 为什么不另开表：M16 schema 已经创建了 email_verifications(token PK, user_id, expires_at, used_at)
// 完全契合本接口语义；额外开表只会造成"两张同义表"的运维负担。
type PgActivationTokenStore struct {
	pool *pgxpool.Pool
}

// NewPgActivationTokenStore 构造仓库。pool 由 caller 维护生命周期。
func NewPgActivationTokenStore(pool *pgxpool.Pool) *PgActivationTokenStore {
	return &PgActivationTokenStore{pool: pool}
}

// Put 实现接口。
func (r *PgActivationTokenStore) Put(ctx context.Context, t *ActivationToken) error {
	if t == nil || t.Token == "" || t.UserID == "" {
		return errors.New("auth.PgActivationTokenStore.Put: empty token/user_id")
	}
	if t.ExpiresAt.IsZero() {
		return errors.New("auth.PgActivationTokenStore.Put: empty expires_at")
	}
	q := `INSERT INTO email_verifications (token, user_id, expires_at)
	      VALUES ($1, $2::uuid, $3)`
	_, err := r.pool.Exec(ctx, q, t.Token, t.UserID, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("auth.PgActivationTokenStore.Put: %w", err)
	}
	return nil
}

// GetByToken 实现接口。
func (r *PgActivationTokenStore) GetByToken(ctx context.Context, token string) (*ActivationToken, error) {
	if token == "" {
		return nil, ErrActivationTokenInvalid
	}
	q := `SELECT token, user_id::text, expires_at, COALESCE(used_at, '0001-01-01'::timestamptz)
	      FROM email_verifications WHERE token = $1`
	var (
		tk     ActivationToken
		usedAt time.Time
	)
	err := r.pool.QueryRow(ctx, q, token).Scan(&tk.Token, &tk.UserID, &tk.ExpiresAt, &usedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrActivationTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("auth.PgActivationTokenStore.GetByToken: %w", err)
	}
	if !usedAt.IsZero() && usedAt.Year() > 1 {
		tk.UsedAt = usedAt
		return &tk, ErrActivationTokenUsed
	}
	if !tk.ExpiresAt.IsZero() && time.Now().After(tk.ExpiresAt) {
		return &tk, ErrActivationTokenExpired
	}
	return &tk, nil
}

// MarkUsed 实现接口。
func (r *PgActivationTokenStore) MarkUsed(ctx context.Context, token string, at time.Time) error {
	if token == "" {
		return ErrActivationTokenInvalid
	}
	if at.IsZero() {
		at = time.Now()
	}
	q := `UPDATE email_verifications SET used_at = $1 WHERE token = $2 AND used_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, at, token)
	if err != nil {
		return fmt.Errorf("auth.PgActivationTokenStore.MarkUsed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrActivationTokenInvalid
	}
	return nil
}

// DeleteByUser 实现接口。
func (r *PgActivationTokenStore) DeleteByUser(ctx context.Context, userID string) (int, error) {
	if userID == "" {
		return 0, errors.New("auth.PgActivationTokenStore.DeleteByUser: empty user_id")
	}
	q := `DELETE FROM email_verifications WHERE user_id = $1::uuid AND used_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, userID)
	if err != nil {
		return 0, fmt.Errorf("auth.PgActivationTokenStore.DeleteByUser: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ---- Mem 实现（测试 / 无 PG 部署）----

// MemActivationTokenStore 进程内 map 实现。
type MemActivationTokenStore struct {
	mu     sync.Mutex
	tokens map[string]*ActivationToken
}

// NewMemActivationTokenStore 构造空仓库。
func NewMemActivationTokenStore() *MemActivationTokenStore {
	return &MemActivationTokenStore{tokens: map[string]*ActivationToken{}}
}

// Put 实现接口。
func (r *MemActivationTokenStore) Put(_ context.Context, t *ActivationToken) error {
	if t == nil || t.Token == "" {
		return errors.New("auth.MemActivationTokenStore.Put: empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *t
	r.tokens[t.Token] = &cp
	return nil
}

// GetByToken 实现接口。
func (r *MemActivationTokenStore) GetByToken(_ context.Context, token string) (*ActivationToken, error) {
	if token == "" {
		return nil, ErrActivationTokenInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tokens[token]
	if !ok {
		return nil, ErrActivationTokenInvalid
	}
	cp := *t
	if !cp.UsedAt.IsZero() {
		return &cp, ErrActivationTokenUsed
	}
	if !cp.ExpiresAt.IsZero() && time.Now().After(cp.ExpiresAt) {
		return &cp, ErrActivationTokenExpired
	}
	return &cp, nil
}

// MarkUsed 实现接口。
func (r *MemActivationTokenStore) MarkUsed(_ context.Context, token string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tokens[token]
	if !ok {
		return ErrActivationTokenInvalid
	}
	if !t.UsedAt.IsZero() {
		return ErrActivationTokenUsed
	}
	if at.IsZero() {
		at = time.Now()
	}
	t.UsedAt = at
	return nil
}

// DeleteByUser 实现接口。
func (r *MemActivationTokenStore) DeleteByUser(_ context.Context, userID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for k, t := range r.tokens {
		if t.UserID == userID && t.UsedAt.IsZero() {
			delete(r.tokens, k)
			n++
		}
	}
	return n, nil
}
