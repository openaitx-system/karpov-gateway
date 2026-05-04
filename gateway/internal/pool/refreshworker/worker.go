// Package refreshworker 实现凭据池主动续期后台 worker.
//
// 与 healthworker 解耦的原因:
//   - healthworker 关注"凭据是否能用" (HealthCheck), 周期短 (5 min);
//   - refreshworker 关注"凭据是否快过期" (TTL), 周期长 (默认 30 min), 提前续期.
//
// 触发条件 (按 provider 区分, 凭据不通用):
//   - QQ 音乐: 解析 payload 里的 `musickey_create_time + key_expires_in` 算出 ExpiresAt;
//     ExpiresAt - now < BeforeExpiryThreshold (默认 2h) 即提前续期.
//   - 网易云: cookie 没显式 TTL, 兜底按 LastUsedAt + NeteaseInterval (默认 24h)
//     周期续期 (调 /api/login/token/refresh 拉新 cookie).
//
// 调度模型: 用 ants v2 worker pool, 同时只跑 N 个 refresh; 单条凭据最大耗时
// PerRefreshTimeout (默认 30s). 失败不影响其他凭据 — provider/credential 隔离.
package refreshworker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"

	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// Options 控制 RefreshWorker 行为.
type Options struct {
	// Interval 主动续期扫描周期, 默认 30 分钟.
	Interval time.Duration
	// BeforeExpiryThreshold 凭据剩余 TTL 小于该值时触发续期, 默认 2 小时.
	BeforeExpiryThreshold time.Duration
	// NeteaseInterval 没显式 TTL 的 provider (网易云) 的兜底周期续期阈值, 默认 24 小时.
	NeteaseInterval time.Duration
	// PerRefreshTimeout 单次 RefreshFunc 调用超时, 默认 30 秒.
	PerRefreshTimeout time.Duration
	// PoolSize ants worker pool 容量, 默认 4. 续期是写操作, 不要把上游打爆.
	PoolSize int
	// Logger 默认 slog.Default().
	Logger *slog.Logger
	// Clock 测试注入; 默认 time.Now.
	Clock func() time.Time
}

// Worker 是凭据主动续期后台.
type Worker struct {
	pool       *pool.Service
	repo       pool.Repo
	refreshFns map[string]pool.RefreshFunc // provider name → 续期函数 (只读快照)
	opts       Options

	antsPool *ants.Pool

	// inflight 防止同一凭据被重复提交 (上轮还没跑完就到下一轮 tick).
	inflight sync.Map // credID → struct{}

	// mu 仅用于 RunOnce 跨调用并发保护.
	mu sync.Mutex
}

// New 构造 RefreshWorker.
//
// refreshFns 应至少含 "qqmusic" / "netease" 两个键; 没注册的 provider 会被静默跳过.
// 这是"provider 隔离"的具象: 没续期函数 = 不知道怎么刷, 索性不动.
func New(p *pool.Service, repo pool.Repo, refreshFns map[string]pool.RefreshFunc, opts Options) (*Worker, error) {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Minute
	}
	if opts.BeforeExpiryThreshold <= 0 {
		opts.BeforeExpiryThreshold = 2 * time.Hour
	}
	if opts.NeteaseInterval <= 0 {
		opts.NeteaseInterval = 24 * time.Hour
	}
	if opts.PerRefreshTimeout <= 0 {
		opts.PerRefreshTimeout = 30 * time.Second
	}
	if opts.PoolSize <= 0 {
		opts.PoolSize = 4
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	// 拷贝 refreshFns (调用方无意中改 map 不影响 worker)
	fns := make(map[string]pool.RefreshFunc, len(refreshFns))
	for k, v := range refreshFns {
		fns[k] = v
	}

	ap, err := ants.NewPool(opts.PoolSize,
		ants.WithPreAlloc(false),
		ants.WithNonblocking(false),
		ants.WithPanicHandler(func(p any) {
			opts.Logger.Error("refreshworker ants panic", "err", p)
		}),
	)
	if err != nil {
		return nil, err
	}
	return &Worker{pool: p, repo: repo, refreshFns: fns, opts: opts, antsPool: ap}, nil
}

// Close 释放 ants pool.
func (w *Worker) Close() {
	if w.antsPool != nil {
		w.antsPool.Release()
	}
}

// Run 阻塞循环; ctx 取消即退出. 启动时立即跑一次.
func (w *Worker) Run(ctx context.Context) error {
	w.opts.Logger.Info("refresh worker starting",
		"interval", w.opts.Interval,
		"before_expiry", w.opts.BeforeExpiryThreshold,
		"netease_interval", w.opts.NeteaseInterval,
		"pool_size", w.opts.PoolSize,
		"providers", providerNames(w.refreshFns))
	w.RunOnce(ctx)
	t := time.NewTicker(w.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.opts.Logger.Info("refresh worker stopped", "reason", ctx.Err())
			w.Close()
			return ctx.Err()
		case <-t.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce 跑一轮主动续期扫描. 可重入, 测试可直接调.
//
// 算法:
//  1. 对每个注册了 RefreshFunc 的 provider, 从 repo.List(provider) 拉所有 active 凭据
//     (provider 隔离已经在 repo 层 WHERE provider=$1 强制).
//  2. 对每条凭据按 provider 类型判断是否需要刷新 (shouldRefresh).
//  3. 命中的丢进 ants pool 并发跑 refreshOne.
func (w *Worker) RunOnce(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.opts.Clock()
	var wg sync.WaitGroup
	for providerName, fn := range w.refreshFns {
		creds, err := w.repo.List(ctx, providerName, provider.CapNone)
		if err != nil {
			w.opts.Logger.Warn("refresh: list credentials failed",
				"provider", providerName, "err", err)
			continue
		}
		for _, c := range creds {
			if c.Status != pool.StatusActive {
				continue
			}
			if !w.shouldRefresh(c, now) {
				continue
			}
			// 防重: inflight 没释放就跳过本轮
			if _, loaded := w.inflight.LoadOrStore(c.ID, struct{}{}); loaded {
				w.opts.Logger.Debug("refresh: skip in-flight", "cred", c.ID)
				continue
			}
			c := c
			fn := fn
			pn := providerName
			wg.Add(1)
			task := func() {
				defer wg.Done()
				defer w.inflight.Delete(c.ID)
				w.refreshOne(ctx, pn, c, fn)
			}
			if err := w.antsPool.Submit(task); err != nil {
				// pool 满载: 退化为同步避免漏刷
				w.opts.Logger.Warn("refresh: ants submit failed, fallback to inline",
					"provider", pn, "cred", c.ID, "err", err)
				task()
			}
		}
	}
	wg.Wait()
}

// shouldRefresh 按 provider 决定凭据是否需要主动续期.
//
//   - QQ 音乐: payload 含 `musickey_create_time + key_expires_in`, 用 pool.ParseExpiresAt
//     精确算 ExpiresAt; 距离过期 < BeforeExpiryThreshold 即续期. 解析失败 (字段缺) 时
//     不续期 (不知道何时过期, 让 healthworker 兜底).
//   - 网易云: cookie 没显式 TTL, 走 LastUsedAt 兜底; 距上次用过 > NeteaseInterval 即续期.
//     特殊场景 LastUsedAt 为零 (从没用过) 用 CreatedAt.
func (w *Worker) shouldRefresh(c pool.Credential, now time.Time) bool {
	// 优先走显式 expires_at (QQ 音乐路径)
	expiresAt := pool.ParseExpiresAt(c.Payload)
	if !expiresAt.IsZero() {
		remaining := expiresAt.Sub(now)
		return remaining > 0 && remaining < w.opts.BeforeExpiryThreshold
	}

	// 没显式 expiry: 兜底周期续期 (网易云 cookie / 其他无 TTL provider)
	last := c.LastUsedAt
	if last.IsZero() {
		last = c.CreatedAt
	}
	if last.IsZero() {
		// 没任何时间戳: 不主动刷 (避免每轮都打)
		return false
	}
	return now.Sub(last) >= w.opts.NeteaseInterval
}

// refreshOne 调 pool.RefreshCredential, 让 pool service 跑状态机
// (StatusRefreshing → 调 fn → 写回 payload + StatusActive).
func (w *Worker) refreshOne(ctx context.Context, providerName string, c pool.Credential, fn pool.RefreshFunc) {
	rctx, cancel := context.WithTimeout(ctx, w.opts.PerRefreshTimeout)
	defer cancel()

	expiresAt := pool.ParseExpiresAt(c.Payload)
	w.opts.Logger.Info("refresh: start",
		"cred", c.ID, "provider", providerName, "expires_at", expiresAt)

	_, err := w.pool.RefreshCredential(rctx, c.ID, fn)
	if err != nil {
		// "already refreshing" 表示并发触发到了 — 不是错误.
		if errors.Is(err, context.Canceled) {
			return
		}
		w.opts.Logger.Warn("refresh: failed",
			"cred", c.ID, "provider", providerName, "err", err)
		return
	}
	w.opts.Logger.Info("refresh: success",
		"cred", c.ID, "provider", providerName)
}

func providerNames(m map[string]pool.RefreshFunc) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
