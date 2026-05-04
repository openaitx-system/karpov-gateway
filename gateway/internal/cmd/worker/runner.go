// Package worker 是 health-check + refresh worker 的 wiring runner.
//
// **配置优先级**: CLI flag > MGW_WORKER_<KEY> > MGW_<KEY> > legacy env > default
//
// Worker 同时跑两个后台 (用 ants v2 worker pool 控制并发):
//   - healthworker: 周期 5 min, 检测凭据是否仍可用, 三连失败 → disable.
//   - refreshworker: 周期 30 min, 主动续期; QQ 音乐按 expires_at - now < 2h 触发,
//     网易云按 LastUsedAt + 24h 兜底. 两 worker 共用 pool.Service / repo, 凭据按
//     provider 隔离 (repo.List(provider) 在 SQL 层 WHERE provider=$1).
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	pkggateway "github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool/healthworker"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool/refreshworker"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/netease"
	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusicprovider"
	"github.com/MiChongs/QQMusicApi/gateway/internal/store"
	"github.com/MiChongs/QQMusicApi/gateway/migrations"
)

// Version 由 ldflags 注入.
var Version = "v0.3.0-dev"

const pgConnectTimeout = 10 * time.Second

// Run 启动 Worker; 阻塞到 ctx 取消.
func Run(ctx context.Context, args []string) error {
	l := cmdpkg.NewLoader("worker")
	l.String("pg", "", "PostgreSQL DSN (empty = in-memory repo)")
	l.Duration("interval", 5*time.Minute, "health check interval")
	l.Int("threshold", 3, "consecutive failures before disabling a credential")
	l.Duration("timeout", 5*time.Second, "single HealthCheck RPC timeout")
	l.Int("hc-pool-size", 8, "ants pool size for concurrent health checks")
	l.Duration("refresh-interval", 30*time.Minute, "proactive refresh scan interval")
	l.Duration("refresh-before-expiry", 2*time.Hour, "refresh QQMusic credentials when remaining TTL is under this")
	l.Duration("refresh-netease-interval", 24*time.Hour, "refresh netease cookies older than this (no explicit TTL)")
	l.Duration("refresh-timeout", 30*time.Second, "single refresh call timeout")
	l.Int("refresh-pool-size", 4, "ants pool size for concurrent refreshes")
	l.Bool("version", false, "print version and exit")
	l.LegacyEnv("pg", "DATABASE_URL", "POSTGRES_DSN")
	if stop, err := l.Parse(args); err != nil {
		return err
	} else if stop {
		return nil
	}
	if l.GetBool("version") {
		fmt.Println(Version)
		return nil
	}

	pgDSN := cmdpkg.ComposePGDSN(l.GetString("pg"))
	hcInterval := l.GetDuration("interval")
	threshold := l.GetInt("threshold")
	hcTimeout := l.GetDuration("timeout")
	hcPoolSize := l.GetInt("hc-pool-size")
	rInterval := l.GetDuration("refresh-interval")
	rBefore := l.GetDuration("refresh-before-expiry")
	rNetInterval := l.GetDuration("refresh-netease-interval")
	rTimeout := l.GetDuration("refresh-timeout")
	rPoolSize := l.GetInt("refresh-pool-size")

	logger := observability.NewLogger("pool-worker", slog.LevelInfo)
	logger.Info("worker starting",
		"version", Version, "pg", pgDSN != "",
		"hc_interval", hcInterval, "hc_pool_size", hcPoolSize,
		"refresh_interval", rInterval, "refresh_pool_size", rPoolSize,
		"refresh_before_expiry", rBefore, "refresh_netease_interval", rNetInterval)

	// ---- Repo ----
	var repo pool.Repo
	if pgDSN == "" {
		repo = pool.NewMemRepo()
		logger.Warn("in-memory repo: nothing to health-check on a fresh worker (use -pg for production)")
	} else {
		if err := store.MigrateUp(ctx, migrations.FS, pgDSN, "pool"); err != nil {
			logger.Error("pool migrations failed", "err", err)
			return fmt.Errorf("pool migrations: %w", err)
		}
		dialCtx, dialCancel := context.WithTimeout(ctx, pgConnectTimeout)
		defer dialCancel()
		pgPool, err := store.NewPGPool(dialCtx, store.PGConfig{DSN: pgDSN})
		if err != nil {
			return fmt.Errorf("connect PG: %w", err)
		}
		pg, err := pool.NewPgRepo(pgPool)
		if err != nil {
			return fmt.Errorf("init PG repo: %w", err)
		}
		repo = pg
		logger.Info("using PG repo")
	}

	psvc := pool.NewService(repo, pool.Options{})

	// ---- Provider Registry (健康检查需要 qqmusic + netease 双 provider) ----
	reg := provider.NewRegistry()
	qqClient := qqmusic.NewClient(qqmusic.ClientOptions{})
	if err := qqmusicprovider.Register(reg, qqClient); err != nil {
		return fmt.Errorf("register qqmusic provider: %w", err)
	}
	if err := netease.Register(reg, netease.NewClient(netease.ClientOptions{})); err != nil {
		return fmt.Errorf("register netease provider: %w", err)
	}

	// ---- Refresh Funcs (与 gateway runner 注册的同源, 凭据按 provider 隔离) ----
	refreshFns := map[string]pool.RefreshFunc{
		"qqmusic": pkggateway.NewQQMusicRefresher(qqClient),
		"netease": pkggateway.NewNeteaseRefresher(),
	}

	// ---- Health Worker (ants 并发) ----
	hw, err := healthworker.New(psvc, repo, reg, healthworker.Options{
		Interval:         hcInterval,
		FailureThreshold: threshold,
		CheckTimeout:     hcTimeout,
		PoolSize:         hcPoolSize,
		Logger:           logger,
	})
	if err != nil {
		return fmt.Errorf("init health worker: %w", err)
	}

	// ---- Refresh Worker (ants 并发, provider-specific) ----
	rw, err := refreshworker.New(psvc, repo, refreshFns, refreshworker.Options{
		Interval:              rInterval,
		BeforeExpiryThreshold: rBefore,
		NeteaseInterval:       rNetInterval,
		PerRefreshTimeout:     rTimeout,
		PoolSize:              rPoolSize,
		Logger:                logger,
	})
	if err != nil {
		return fmt.Errorf("init refresh worker: %w", err)
	}

	// 两 worker 并行跑; 任一失败都触发 ctx 取消, errgroup 返回首个 error.
	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		err := hw.Run(egCtx)
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("health worker: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		err := rw.Run(egCtx)
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("refresh worker: %w", err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("worker shutdown complete")
	return nil
}
