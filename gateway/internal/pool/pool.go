// Package pool 实现凭据池（号池）调度。
//
// 服务接口（与 Plan §8.1 / api/proto/v1/pool.proto 对齐）：
//
//	Acquire(ctx, provider, capability) (*CredentialLease, error)
//	Release(lease, result PoolResult)
//	AddCredential / RemoveCredential / HealthSummary
//
// 当前实现 = 内存版后端 + 加权随机选择 + 锁/冷却/封禁状态机。
// PG 持久化在 M16 落地（实现 Repo 接口的 PG 适配器即可，本文件零改动）。
package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// Status 是凭据状态机。
type Status string

const (
	StatusActive     Status = "active"
	StatusDisabled   Status = "disabled"
	StatusBanned     Status = "banned"
	StatusRefreshing Status = "refreshing"
)

// Credential 是 Pool 内部凭据快照（持久化到 PG 时按 §6.4 schema 落库）。
type Credential struct {
	ID            string
	Provider      string
	Label         string
	Payload       []byte // AES-256-GCM 解密后的明文（KEK 在外层处理）
	Capabilities  []provider.Capability
	Status        Status
	HealthScore   float64 // [0, 1]
	FailCount     int
	LastUsedAt    time.Time
	LastFailedAt  time.Time
	CooldownUntil time.Time
	CreatedAt     time.Time // 入池时间，便于 UI 展示
}

// Repo 是 Pool 的存储抽象；in-memory 与 PG 实现同一接口。
type Repo interface {
	Add(ctx context.Context, c Credential) error
	Remove(ctx context.Context, id string) error
	List(ctx context.Context, providerName string, cap provider.Capability) ([]Credential, error)
	Update(ctx context.Context, c Credential) error
	Get(ctx context.Context, id string) (Credential, error)
}

// Service 是 Pool 业务入口。
//
// 并发模型：所有公开方法都是并发安全的。内部用 sync.Mutex 保护单凭据写竞争；
// 选择算法（weighted random）只读 List 不写，扩展性好。
type Service struct {
	repo  Repo
	mu    sync.Mutex
	clock func() time.Time
	rng   *rand.Rand
}

// Options 控制 Service 构造。
type Options struct {
	Clock func() time.Time // 默认 time.Now，测试可注入
	Rand  *rand.Rand       // 默认按时间种子；测试可注入确定性
}

// NewService 构造 Pool 服务。
func NewService(repo Repo, opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0xDEADBEEF))
	}
	return &Service{repo: repo, clock: opts.Clock, rng: opts.Rand}
}

// AcquireOptions 控制 Acquire 行为。
type AcquireOptions struct {
	Capability provider.Capability
}

// ErrNoCandidate 表示当前没有可用凭据（全部 banned/disabled/cooldown）。
var ErrNoCandidate = errors.New("pool: no candidate credential available")

// Acquire 选一张可用凭据并返回 lease。
//
// 算法：从 repo 取候选 → 过滤 status=active 且 cooldown 已过 → 加权随机选取
// （score = HealthScore × 1/(failed_recently+1)）→ 标记 LastUsedAt。Release 时
// 根据结果调整健康度。
func (s *Service) Acquire(ctx context.Context, providerName string, opts AcquireOptions) (*provider.CredentialLease, error) {
	candidates, err := s.repo.List(ctx, providerName, opts.Capability)
	if err != nil {
		return nil, fmt.Errorf("pool.Acquire: %w", err)
	}
	now := s.clock()

	type weighted struct {
		c Credential
		w float64
	}
	pool := make([]weighted, 0, len(candidates))
	for _, c := range candidates {
		if c.Status != StatusActive {
			continue
		}
		if now.Before(c.CooldownUntil) {
			continue
		}
		score := c.HealthScore
		if score <= 0 {
			score = 0.01 // 给极低分凭据保留极小概率被选中
		}
		penalty := 1.0 / float64(c.FailCount+1)
		pool = append(pool, weighted{c: c, w: score * penalty})
	}
	if len(pool) == 0 {
		return nil, ErrNoCandidate
	}

	// 加权随机
	total := 0.0
	for _, p := range pool {
		total += p.w
	}
	target := s.rng.Float64() * total
	var picked Credential
	cum := 0.0
	for _, p := range pool {
		cum += p.w
		if target <= cum {
			picked = p.c
			break
		}
	}

	// 标记 LastUsedAt
	picked.LastUsedAt = now
	if err := s.repo.Update(ctx, picked); err != nil {
		return nil, fmt.Errorf("pool.Acquire: mark used: %w", err)
	}

	leaseID := picked.ID
	return &provider.CredentialLease{
		ID:       leaseID,
		Provider: picked.Provider,
		Payload:  picked.Payload,
		Release: func(result provider.PoolResult) {
			s.release(context.Background(), leaseID, result)
		},
	}, nil
}

// ReleaseByCredentialID 是 release 的导出版本：按 credential ID 应用状态转换。
//
// 用于跨副本 Pool 服务（M36 Redis lease registry）：当 Release RPC 落到一个
// 不持有原始 lease 闭包的 Pod 时，从 Redis 拿到 credID 后调本方法即可正确
// 更新健康度。同进程路径仍走 Acquire 返回的 lease.Release 闭包。
func (s *Service) ReleaseByCredentialID(ctx context.Context, credID string, result provider.PoolResult) {
	s.release(ctx, credID, result)
}

// release 等价 Plan §8.1 中描述的状态转换。
//
// OK            -> health += 0.01 (cap 1.0); fail_count = 0
// RateLimited   -> cooldown_until = now+5min; health *= 0.8
// AuthFailed    -> status = 'banned' （永久下线）
// NetworkError  -> fail_count++; if >=5 then cooldown 1min
func (s *Service) release(ctx context.Context, id string, result provider.PoolResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return // 静默丢弃；通常是 Add/Remove 竞态，不影响业务
	}
	now := s.clock()
	switch result {
	case provider.PoolResultOK:
		c.HealthScore += 0.01
		if c.HealthScore > 1.0 {
			c.HealthScore = 1.0
		}
		c.FailCount = 0
	case provider.PoolResultRateLimited:
		c.CooldownUntil = now.Add(5 * time.Minute)
		c.HealthScore *= 0.8
	case provider.PoolResultAuthFailed:
		c.Status = StatusBanned
	case provider.PoolResultNetworkError:
		c.FailCount++
		c.LastFailedAt = now
		if c.FailCount >= 5 {
			c.CooldownUntil = now.Add(1 * time.Minute)
			c.FailCount = 0
		}
	}
	_ = s.repo.Update(ctx, c)
}

// AddCredential 加入新凭据；初始 HealthScore=1.0、Status=active、CreatedAt=now。
func (s *Service) AddCredential(ctx context.Context, c Credential) error {
	if c.HealthScore == 0 {
		c.HealthScore = 1.0
	}
	if c.Status == "" {
		c.Status = StatusActive
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = s.clock()
	}
	return s.repo.Add(ctx, c)
}

// RemoveCredential 物理删除（实际产线建议用 status=disabled 软删）。
func (s *Service) RemoveCredential(ctx context.Context, id string) error {
	return s.repo.Remove(ctx, id)
}

// GetCredential 取单条凭据（管理面用，列表后回填或状态切换后回读）。
func (s *Service) GetCredential(ctx context.Context, id string) (Credential, error) {
	return s.repo.Get(ctx, id)
}

// SetCredentialStatus 是管理面状态切换：active ↔ disabled。
//
// 安全约束：
//   - 不接受 status="banned"——这是 healthworker 自动熔断使用的状态，
//     人工设 banned 没有正常恢复路径；如需禁用就用 disabled。
//   - 切到 active 时同步清空 cooldown（运维手动复活）；切到 disabled
//     不清空，便于诊断历史失败时间窗。
//   - 未知状态返回 ErrInvalidStatus 让 caller 决定 4xx/5xx。
func (s *Service) SetCredentialStatus(ctx context.Context, id string, st Status) error {
	if st != StatusActive && st != StatusDisabled {
		return ErrInvalidStatus
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("pool.SetCredentialStatus: %w", err)
	}
	c.Status = st
	if st == StatusActive {
		c.CooldownUntil = time.Time{}
		c.FailCount = 0
	}
	if err := s.repo.Update(ctx, c); err != nil {
		return fmt.Errorf("pool.SetCredentialStatus: %w", err)
	}
	return nil
}

// ErrInvalidStatus 来自 SetCredentialStatus：仅允许 active / disabled。
var ErrInvalidStatus = errors.New("pool: invalid status (allowed: active, disabled)")

// DisableCredential 软删凭据（标记 Status=disabled）。
//
// 比 RemoveCredential 安全：保留历史 health/fail_count，便于运维事后排查。
// 健康检查 worker 在连续失败到阈值时调用本方法。
func (s *Service) DisableCredential(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("pool.DisableCredential: %w", err)
	}
	c.Status = StatusDisabled
	if err := s.repo.Update(ctx, c); err != nil {
		return fmt.Errorf("pool.DisableCredential: %w", err)
	}
	return nil
}

// HealthSummary 返回 provider 下凭据健康概览（active 数量、平均 score、被禁数量）。
type HealthSummary struct {
	Provider     string
	ActiveCount  int
	BannedCount  int
	AverageScore float64
}

// ListCredentialsOptions 控制 ListCredentials 返回结果的过滤与分页。
//
//   - Provider 空 → 所有 provider；
//   - StatusFilter 空 → 不过滤（含 active/disabled/banned 等）；
//   - Limit ≤ 0 → 默认 100，最大 500；Offset 可作分页跳过。
type ListCredentialsOptions struct {
	Provider     string
	StatusFilter string
	Limit        int
	Offset       int
}

// ListCredentials 列出符合条件的凭证；总数（不含分页）单独返回，前端分页友好。
//
// 实现取 repo.List + 内存过滤；号池规模通常 < 1000，无需在 repo 层做复杂 SQL。
// 状态过滤接受 "active" / "disabled" / "banned" / "" (=不过滤)；其他值忽略。
func (s *Service) ListCredentials(ctx context.Context, opts ListCredentialsOptions) ([]Credential, int, error) {
	all, err := s.repo.List(ctx, opts.Provider, provider.CapNone)
	if err != nil {
		return nil, 0, err
	}
	var filtered []Credential
	if opts.StatusFilter != "" {
		want := Status(opts.StatusFilter)
		for _, c := range all {
			if c.Status == want {
				filtered = append(filtered, c)
			}
		}
	} else {
		filtered = all
	}
	// MemRepo 的 List 来自 map 迭代，顺序不稳定；分页必须 deterministic：
	// 主键排序：ID（保证生产 PG 实现也用 ORDER BY id 保持一致语义）。
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
	total := len(filtered)
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	if opts.Offset >= total {
		return []Credential{}, total, nil
	}
	end := opts.Offset + limit
	if end > total {
		end = total
	}
	return filtered[opts.Offset:end], total, nil
}

// HealthSummary 等价 PoolService.HealthSummary。
func (s *Service) HealthSummary(ctx context.Context, providerName string) (HealthSummary, error) {
	all, err := s.repo.List(ctx, providerName, provider.CapNone)
	if err != nil {
		return HealthSummary{}, err
	}
	out := HealthSummary{Provider: providerName}
	var sum float64
	for _, c := range all {
		switch c.Status {
		case StatusActive:
			out.ActiveCount++
			sum += c.HealthScore
		case StatusBanned:
			out.BannedCount++
		}
	}
	if out.ActiveCount > 0 {
		out.AverageScore = sum / float64(out.ActiveCount)
	}
	return out, nil
}

// RefreshFunc 接受旧 payload（解密后 JSON），调上游 API 刷新，返回新 payload。
type RefreshFunc func(ctx context.Context, oldPayload []byte) (newPayload []byte, err error)

// RefreshCredential 主动刷新凭据：标记 refreshing → 调 API → 更新 payload 并恢复 active。
// 使用乐观锁模式，长耗时 API 调用期间不持有 mu，避免阻塞全局。
func (s *Service) RefreshCredential(ctx context.Context, id string, fn RefreshFunc) (Credential, error) {
	// Phase 1: 标记 refreshing（短锁）
	s.mu.Lock()
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return c, fmt.Errorf("pool.RefreshCredential: %w", err)
	}
	if c.Status == StatusRefreshing {
		s.mu.Unlock()
		return c, errors.New("pool.RefreshCredential: already refreshing")
	}
	origStatus := c.Status
	c.Status = StatusRefreshing
	_ = s.repo.Update(ctx, c)
	s.mu.Unlock()

	// Phase 2: 调上游 API（不持锁）
	newPayload, refreshErr := fn(ctx, c.Payload)

	// Phase 3: 写结果（短锁）
	s.mu.Lock()
	defer s.mu.Unlock()
	c, _ = s.repo.Get(ctx, id)
	if refreshErr != nil {
		c.Status = origStatus
		_ = s.repo.Update(ctx, c)
		return c, fmt.Errorf("pool.RefreshCredential: %w", refreshErr)
	}
	c.Payload = newPayload
	c.Status = StatusActive
	c.HealthScore = 1.0
	c.FailCount = 0
	c.CooldownUntil = time.Time{}
	if err := s.repo.Update(ctx, c); err != nil {
		return c, fmt.Errorf("pool.RefreshCredential: %w", err)
	}
	return c, nil
}

// ParseExpiresAt 从解密的凭据 payload JSON 提取过期时间。
// 计算方式与 Python `Credential.is_expired` 对齐：musickey_create_time + key_expires_in。
func ParseExpiresAt(payload []byte) time.Time {
	var parsed struct {
		MusicKeyCreateTime int64 `json:"musickeyCreateTime"`
		KeyExpiresIn       int64 `json:"keyExpiresIn"`
		// 兼容 snake_case
		MusicKeyCreateTime2 int64 `json:"musickey_create_time"`
		KeyExpiresIn2       int64 `json:"key_expires_in"`
	}
	if json.Unmarshal(payload, &parsed) != nil {
		return time.Time{}
	}
	create := parsed.MusicKeyCreateTime
	if create == 0 {
		create = parsed.MusicKeyCreateTime2
	}
	ttl := parsed.KeyExpiresIn
	if ttl == 0 {
		ttl = parsed.KeyExpiresIn2
	}
	if create == 0 || ttl == 0 {
		return time.Time{}
	}
	return time.Unix(create+ttl, 0)
}

// NeedsRefresh 判断凭据是否需要刷新（已消耗 TTL 的 threshold 比例）。
func NeedsRefresh(payload []byte, threshold float64) bool {
	var parsed struct {
		MusicKeyCreateTime  int64 `json:"musickeyCreateTime"`
		KeyExpiresIn        int64 `json:"keyExpiresIn"`
		MusicKeyCreateTime2 int64 `json:"musickey_create_time"`
		KeyExpiresIn2       int64 `json:"key_expires_in"`
	}
	if json.Unmarshal(payload, &parsed) != nil {
		return false
	}
	create := parsed.MusicKeyCreateTime
	if create == 0 {
		create = parsed.MusicKeyCreateTime2
	}
	ttl := parsed.KeyExpiresIn
	if ttl == 0 {
		ttl = parsed.KeyExpiresIn2
	}
	if create == 0 || ttl == 0 {
		return false
	}
	elapsed := time.Since(time.Unix(create, 0)).Seconds()
	return elapsed >= float64(ttl)*threshold
}
