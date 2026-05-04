// Package healthworker 实现凭据池健康检查后台 worker.
//
// 职责 (Plan §8.1):
//   - 周期 (默认 5 分钟) 遍历所有 active 凭证, 用 ants v2 worker pool 并发调用 provider.HealthCheck
//   - 成功: 通过 pool.Service.ReleaseByCredentialID(OK) 让健康分自然回升
//   - 失败: ReleaseByCredentialID(NetworkError) + worker 内存计数; 连续 N
//     次 (默认 3) 失败 → pool.Service.DisableCredential 软删, 避免 banned
//     之外的"间歇性故障凭证"长期占调度槽
//
// 设计取舍:
//   - 用 ants v2 控制并发数: 100 条凭据避免一次性 100 个 goroutine 打爆上游, PoolSize 默认 8
//   - 在 worker 内部跟踪连续失败数 (sync.Map), 与 pool.Service 内置的
//     fail_count 解耦: 后者属于"客户端请求失败", 本计数属于"健康探测失败",
//     语义不同
//   - provider 不通用: 凭据从 repo.List(provider, cap) 取出时已经按 provider 过滤,
//     不会把 qqmusic 凭据塞给 netease HealthCheck
package healthworker

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

// Options 控制 Worker 行为。
type Options struct {
	// Interval 健康检查周期, 默认 5 分钟.
	Interval time.Duration
	// FailureThreshold 连续失败阈值, 到达即 Disable, 默认 3.
	FailureThreshold int
	// CheckTimeout 单次 HealthCheck 调用超时, 默认 5 秒.
	CheckTimeout time.Duration
	// PoolSize ants worker pool 容量, 默认 8. <=0 时退化为顺序执行 (兼容旧测试).
	PoolSize int
	// Logger 默认 slog.Default().
	Logger *slog.Logger
	// Clock 测试注入; 默认 time.Now.
	Clock func() time.Time
}

// Worker 是凭据健康检查后台.
//
// 并发模型: Run 启动单 goroutine 跑 ticker; RunOnce 内部用 ants.Pool 并发派发 checkOne.
// fails 用 sync.Map 因为多个 ants worker 会并发读写.
type Worker struct {
	pool *pool.Service
	repo pool.Repo
	reg  *provider.Registry
	opts Options

	// fails 记录每个 credID 的连续 HealthCheck 失败次数.
	// 用 sync.Map 因为 ants worker 并发更新; key=credID(string), value=int.
	fails sync.Map

	// antsPool 复用 worker 实例; 关闭由 Close() 触发.
	antsPool *ants.Pool

	// mu 仅用于 RunOnce 跨调用并发保护 (外部直接调用 RunOnce 的场景).
	mu sync.Mutex
}

// New 构造 Worker.
func New(p *pool.Service, repo pool.Repo, reg *provider.Registry, opts Options) (*Worker, error) {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Minute
	}
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = 3
	}
	if opts.CheckTimeout <= 0 {
		opts.CheckTimeout = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	w := &Worker{pool: p, repo: repo, reg: reg, opts: opts}
	if opts.PoolSize > 0 {
		ap, err := ants.NewPool(opts.PoolSize,
			ants.WithPreAlloc(false),
			ants.WithNonblocking(false),
			ants.WithPanicHandler(func(p any) {
				opts.Logger.Error("healthworker ants panic", "err", p)
			}),
		)
		if err != nil {
			return nil, err
		}
		w.antsPool = ap
	}
	return w, nil
}

// Close 释放 ants pool. 与 Run 解耦, 便于热替换 worker 配置时干净退出.
func (w *Worker) Close() {
	if w.antsPool != nil {
		w.antsPool.Release()
	}
}

// Run 阻塞循环; ctx 取消即退出. 会在启动时立即跑一次 RunOnce.
func (w *Worker) Run(ctx context.Context) error {
	w.opts.Logger.Info("health worker starting",
		"interval", w.opts.Interval,
		"threshold", w.opts.FailureThreshold,
		"pool_size", w.opts.PoolSize)
	w.RunOnce(ctx)
	t := time.NewTicker(w.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.opts.Logger.Info("health worker stopped", "reason", ctx.Err())
			w.Close()
			return ctx.Err()
		case <-t.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce 跑一轮健康检查; 可重入安全, 方便外部测试 / 强制触发.
//
// 同一轮内, 单 provider 的所有 active 凭据**并行**调用 HealthCheck (ants pool 限并发);
// 不同 provider 之间也是并行 (它们调用各自 provider 的 HealthCheck, 凭据隔离).
func (w *Worker) RunOnce(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var wg sync.WaitGroup
	for _, providerName := range w.reg.Names() {
		prov, ok := w.reg.Get(providerName)
		if !ok {
			continue
		}
		creds, err := w.repo.List(ctx, providerName, provider.CapNone)
		if err != nil {
			w.opts.Logger.Warn("list credentials failed", "provider", providerName, "err", err)
			continue
		}
		for _, c := range creds {
			if c.Status != pool.StatusActive {
				continue
			}
			c := c
			prov := prov
			wg.Add(1)
			task := func() {
				defer wg.Done()
				w.checkOne(ctx, prov, c)
			}
			// 没配 ants pool 时退化为顺序 (保持旧测试行为)
			if w.antsPool == nil {
				task()
				continue
			}
			if err := w.antsPool.Submit(task); err != nil {
				// pool 关闭或满载: 退化为同步执行避免凭据被遗忘
				w.opts.Logger.Warn("ants submit failed, fallback to inline",
					"provider", providerName, "cred", c.ID, "err", err)
				task()
			}
		}
	}
	wg.Wait()
}

// checkOne 跑单凭证的 HealthCheck, 按结果 Release/Disable.
func (w *Worker) checkOne(ctx context.Context, prov provider.MusicProvider, c pool.Credential) {
	checkCtx, cancel := context.WithTimeout(ctx, w.opts.CheckTimeout)
	defer cancel()

	lease := &provider.CredentialLease{
		ID:       c.ID,
		Provider: c.Provider,
		Payload:  c.Payload,
	}

	err := prov.HealthCheck(checkCtx, lease)
	if err == nil {
		w.pool.ReleaseByCredentialID(ctx, c.ID, provider.PoolResultOK)
		// 重置失败计数: 一次成功就清零 (与 Plan §8.1 "三连失败"语义一致).
		w.fails.Delete(c.ID)
		w.opts.Logger.Debug("healthcheck ok", "cred", c.ID, "provider", c.Provider)
		return
	}

	w.pool.ReleaseByCredentialID(ctx, c.ID, provider.PoolResultNetworkError)
	cnt := w.bumpFailure(c.ID)
	w.opts.Logger.Warn("healthcheck failed",
		"cred", c.ID, "provider", c.Provider,
		"streak", cnt, "err", err)

	if cnt >= w.opts.FailureThreshold {
		if disErr := w.pool.DisableCredential(ctx, c.ID); disErr != nil {
			// disable 失败不致命: 下一轮还会再试.
			if !errors.Is(disErr, context.Canceled) {
				w.opts.Logger.Error("disable credential failed",
					"cred", c.ID, "provider", c.Provider, "err", disErr)
			}
			return
		}
		w.opts.Logger.Warn("credential disabled after consecutive failures",
			"cred", c.ID, "provider", c.Provider, "streak", cnt)
		w.fails.Delete(c.ID)
	}
}

// bumpFailure 自增并返回当前连续失败计数. sync.Map 上没原子 inc, 用 LoadOrStore + 重 Store 模拟.
func (w *Worker) bumpFailure(credID string) int {
	for {
		actual, _ := w.fails.LoadOrStore(credID, 0)
		cur, _ := actual.(int)
		next := cur + 1
		if w.fails.CompareAndSwap(credID, cur, next) {
			return next
		}
	}
}

// FailureCount 返回某凭证当前连续失败计数 (测试用).
func (w *Worker) FailureCount(credID string) int {
	v, ok := w.fails.Load(credID)
	if !ok {
		return 0
	}
	n, _ := v.(int)
	return n
}
