package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// APIKeyRecord 是单条 API Key 的持久化记录（DB 行/内存条目）。
//
// 注意：Plain 仅在 Service.CreateAPIKey 返回时填充一次；后续 Repo 操作的
// 实例 Plain 总是空的（DB 不存明文）。
type APIKeyRecord struct {
	ID             string   // 数据库主键
	UserID         string   // 所属用户
	Prefix         string   // 明文前 8 字符
	Hash           string   // argon2id(明文)
	Name           string   // 用户可读标签
	Description    string   // 备注
	PlanID         string   // 绑定套餐（free/basic/pro/enterprise）
	Scopes         []string // ["music:read", ...]
	IPAllow        []string // CIDR 白名单
	RateLimitRPM   int      // 每分钟上限（0=按套餐）
	RateLimitDaily int64    // 每日上限（0=按套餐）
	Enabled        bool     // 可逆禁用
	TotalRequests  int64    // 累计请求
	CreatedAt      time.Time
	ExpiresAt      time.Time
	LastUsedAt     time.Time
	RevokedAt      time.Time
}

// IsActive 判断 key 是否仍可被认证。
func (r *APIKeyRecord) IsActive(now time.Time) bool {
	if !r.RevokedAt.IsZero() {
		return false
	}
	if !r.Enabled {
		return false
	}
	if !r.ExpiresAt.IsZero() && !r.ExpiresAt.After(now) {
		return false
	}
	return true
}

// IsIPAllowed 判断 clientIP 是否在 IPAllow 白名单内。
//
// 语义：
//   - IPAllow 为空 ⇒ 视为通配，任何源 IP 均放行
//   - IPAllow 非空 ⇒ 必须命中至少一条；任一规则解析失败则跳过该条（不影响其它规则判定）
//
// 白名单元素接受三种写法：
//   - "10.0.0.1"          → 单 IPv4，等价 /32
//   - "10.0.0.0/8"        → CIDR
//   - "2001:db8::1"       → 单 IPv6，等价 /128
//
// 解析坏 clientIP 一律拒绝（fail-closed）。
func (r *APIKeyRecord) IsIPAllowed(clientIP string) bool {
	if len(r.IPAllow) == 0 {
		return true
	}
	clientIP = strings.TrimSpace(clientIP)
	// 形如 "1.2.3.4:5678" 的 host:port 取 host
	if h, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = h
	}
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, rule := range r.IPAllow {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if strings.Contains(rule, "/") {
			_, network, err := net.ParseCIDR(rule)
			if err != nil {
				continue
			}
			if network.Contains(ip) {
				return true
			}
			continue
		}
		// 单 IP 字面比较
		ruleIP := net.ParseIP(rule)
		if ruleIP == nil {
			continue
		}
		if ruleIP.Equal(ip) {
			return true
		}
	}
	return false
}

// HasScope 判断 key 是否声明了某个 scope。
//
// 通配规则：
//   - r.Scopes 为空                          ⇒ 通配，任何 scope 通过
//   - 持有 "*"（单星号）                      ⇒ 全通配，任何 scope 通过
//   - 持有 "module:*"，请求 scope = "module:X" ⇒ 通过（模块级通配）
//   - 字面相等                               ⇒ 通过
//
// 校验请求 scope 本身的格式合法性（必须是 module:action）；调用方传入 "*" 或
// "module:*" 这种"通配请求"被视为不合法（不允许"匿名要求所有权限"），返回 false。
func (r *APIKeyRecord) HasScope(scope string) bool {
	if len(r.Scopes) == 0 {
		return true
	}
	// 请求侧不允许带通配（避免攻击者构造 "*:*" 之类绕过）
	if scope == "" || strings.Contains(scope, "*") {
		return false
	}
	requestedModule := scopeModule(scope)
	for _, granted := range r.Scopes {
		if granted == "*" {
			return true
		}
		if granted == scope {
			return true
		}
		// module:* 通配
		if strings.HasSuffix(granted, ":*") {
			gm := granted[:len(granted)-2]
			if gm != "" && gm == requestedModule {
				return true
			}
		}
	}
	return false
}

// HasAllScopes 批量版本：required 都必须满足（同 HasScope 通配规则）。
//
// required 为空切片 ⇒ true（无要求）。
func (r *APIKeyRecord) HasAllScopes(required []string) bool {
	if len(required) == 0 {
		return true
	}
	for _, want := range required {
		if !r.HasScope(want) {
			return false
		}
	}
	return true
}

// scopeModule 取 "module:action" 的 module 部分。
// 不是 "module:action" 形式时返回空串（用于 HasScope 内部，调用前已确认非通配）。
func scopeModule(scope string) string {
	idx := strings.IndexByte(scope, ':')
	if idx <= 0 {
		return ""
	}
	return scope[:idx]
}

// IsValidGrantedScope 校验"颁发给 key 的 scope"格式：
//   - "*"
//   - "module:*"
//   - "module:action"（小写字母）
//
// 中间件不会调用它（颁发时已校验），但前端 / API 层用它做输入校验。
func IsValidGrantedScope(s string) bool {
	if s == "*" {
		return true
	}
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx == len(s)-1 {
		return false
	}
	module := s[:idx]
	action := s[idx+1:]
	if !isLowerAlpha(module) {
		return false
	}
	if action == "*" {
		return true
	}
	return isLowerAlpha(action)
}

func isLowerAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// Status 返回人可读的状态字符串。
func (r *APIKeyRecord) Status(now time.Time) string {
	if !r.RevokedAt.IsZero() {
		return "revoked"
	}
	if !r.Enabled {
		return "disabled"
	}
	if !r.ExpiresAt.IsZero() && !r.ExpiresAt.After(now) {
		return "expired"
	}
	return "active"
}

// APIKeyRepo 是 API Key 的存储抽象。
type APIKeyRepo interface {
	Create(ctx context.Context, k *APIKeyRecord) error
	GetByID(ctx context.Context, id string) (*APIKeyRecord, error)
	GetByPrefix(ctx context.Context, prefix string) ([]*APIKeyRecord, error)
	ListByUser(ctx context.Context, userID string) ([]*APIKeyRecord, error)
	Update(ctx context.Context, k *APIKeyRecord) error
	SetEnabled(ctx context.Context, id, userID string, enabled bool) error
	Revoke(ctx context.Context, id, userID string, at time.Time) error
	Touch(ctx context.Context, id string, at time.Time) error
	IncrTotalRequests(ctx context.Context, id string, delta int64) error
}

// MemAPIKeyRepo 是内存版 APIKeyRepo（开发 / 单元测试）。
type MemAPIKeyRepo struct {
	mu    sync.RWMutex
	store map[string]*APIKeyRecord // id → record
}

// NewMemAPIKeyRepo 构造空 repo。
func NewMemAPIKeyRepo() *MemAPIKeyRepo {
	return &MemAPIKeyRepo{store: make(map[string]*APIKeyRecord)}
}

// Create 实现 APIKeyRepo.Create。同 ID 重复返回错误。
func (r *MemAPIKeyRepo) Create(_ context.Context, k *APIKeyRecord) error {
	if k.ID == "" {
		return errors.New("auth.MemAPIKeyRepo.Create: empty id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.store[k.ID]; dup {
		return errors.New("auth.MemAPIKeyRepo.Create: duplicate id")
	}
	cp := *k
	r.store[k.ID] = &cp
	return nil
}

// GetByID 实现 APIKeyRepo.GetByID。
func (r *MemAPIKeyRepo) GetByID(_ context.Context, id string) (*APIKeyRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.store[id]
	if !ok {
		return nil, ErrAPIKeyNotFound
	}
	cp := *k
	return &cp, nil
}

// Update 实现 APIKeyRepo.Update（全量更新可变字段）。
func (r *MemAPIKeyRepo) Update(_ context.Context, k *APIKeyRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.store[k.ID]
	if !ok {
		return ErrAPIKeyNotFound
	}
	existing.Name = k.Name
	existing.Description = k.Description
	existing.Scopes = append([]string(nil), k.Scopes...)
	existing.IPAllow = append([]string(nil), k.IPAllow...)
	existing.RateLimitRPM = k.RateLimitRPM
	existing.RateLimitDaily = k.RateLimitDaily
	existing.ExpiresAt = k.ExpiresAt
	return nil
}

// SetEnabled 实现 APIKeyRepo.SetEnabled。
func (r *MemAPIKeyRepo) SetEnabled(_ context.Context, id, userID string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.store[id]
	if !ok || k.UserID != userID {
		return ErrAPIKeyNotFound
	}
	k.Enabled = enabled
	return nil
}

// IncrTotalRequests 实现 APIKeyRepo.IncrTotalRequests。
func (r *MemAPIKeyRepo) IncrTotalRequests(_ context.Context, id string, delta int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.store[id]; ok {
		k.TotalRequests += delta
	}
	return nil
}

// GetByPrefix 返回所有匹配 prefix 的 key（理论上 prefix 8 字符不强求唯一；按
// 真实碰撞概率 ~10^-12 ≈ 永远是 1 条，但接口保留多条便于安全侧验签遍历）。
func (r *MemAPIKeyRepo) GetByPrefix(_ context.Context, prefix string) ([]*APIKeyRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*APIKeyRecord
	for _, k := range r.store {
		if k.Prefix == prefix {
			cp := *k
			out = append(out, &cp)
		}
	}
	return out, nil
}

// ListByUser 列出某用户的全部 key（按 CreatedAt 降序）。
func (r *MemAPIKeyRepo) ListByUser(_ context.Context, userID string) ([]*APIKeyRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*APIKeyRecord, 0)
	for _, k := range r.store {
		if k.UserID == userID {
			cp := *k
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Revoke 把 key 标记为已吊销（仅当 userID 匹配；防越权）。
func (r *MemAPIKeyRepo) Revoke(_ context.Context, id, userID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.store[id]
	if !ok {
		return ErrAPIKeyNotFound
	}
	if k.UserID != userID {
		return ErrAPIKeyNotFound
	}
	k.RevokedAt = at
	return nil
}

// Touch 更新 LastUsedAt（验签成功后调用）。
func (r *MemAPIKeyRepo) Touch(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.store[id]; ok {
		k.LastUsedAt = at
		k.TotalRequests++
	}
	return nil
}

// 已知错误。
var (
	ErrAPIKeyNotFound     = errors.New("auth: api key not found")
	ErrAPIKeyExpired      = errors.New("auth: api key expired or revoked")
	ErrAPIKeyInvalid      = errors.New("auth: api key invalid")
	ErrAPIKeyIPNotAllowed = errors.New("auth: api key not allowed from this client ip")
	ErrAPIKeyScopeDenied  = errors.New("auth: api key missing required scope")
	ErrInvalidScope       = errors.New("auth: invalid scope (need * | module:* | module:action, lowercase)")
	ErrInvalidIPAllow     = errors.New("auth: invalid ip/cidr in allowlist")
)

// newAPIKeyID 生成 16 字节 hex 主键。
func newAPIKeyID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
