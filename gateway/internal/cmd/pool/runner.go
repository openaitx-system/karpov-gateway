// Package pool 是 Pool Service 的 wiring runner。
//
// **配置优先级**：CLI flag > MGW_POOL_<KEY> > MGW_<KEY> > legacy env > default
package pool

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	"github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
	pkgpool "github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	pkgstore "github.com/MiChongs/QQMusicApi/gateway/internal/store"
	"github.com/MiChongs/QQMusicApi/gateway/migrations"
)

// Version 由 ldflags 注入。
var Version = "v0.3.0-dev"

const pgConnectTimeout = 10 * time.Second

// Run 启动 Pool Service；阻塞到 ctx 取消。
func Run(ctx context.Context, args []string) error {
	l := cmdpkg.NewLoader("pool")
	l.String("grpc", ":9003", "gRPC listen address")
	l.String("pg", "", "PostgreSQL DSN (empty = use in-memory repo)")
	l.String("lease-redis", "", "Redis addr for lease registry (empty = in-process)")
	l.String("lease-redis-password", "", "Redis password for lease registry")
	l.String("tls-cert", "", "PEM cert (mTLS if set)")
	l.String("tls-key", "", "PEM key")
	l.String("tls-client-ca", "", "PEM client CA bundle")
	l.Bool("version", false, "print version and exit")
	l.LegacyEnv("lease-redis-password", "REDIS_PASSWORD")
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

	grpcAddr := l.GetString("grpc")
	pgDSN := cmdpkg.ComposePGDSN(l.GetString("pg"))
	leaseRedis := cmdpkg.ComposeRedisAddr(l.GetString("lease-redis"))
	leaseRedisPwd := l.GetString("lease-redis-password")
	if leaseRedisPwd == "" {
		leaseRedisPwd = os.Getenv("REDIS_PASSWORD")
	}

	logger := observability.NewLogger("pool-service", slog.LevelInfo)
	logger.Info("pool service starting", "version", Version, "grpc", grpcAddr, "pg", pgDSN != "")

	// ---- Repo 选择 ----
	var repo pkgpool.Repo
	if pgDSN == "" {
		repo = pkgpool.NewMemRepo()
		logger.Info("using in-memory repo (data not persisted)")
	} else {
		if err := pkgstore.MigrateUp(ctx, migrations.FS, pgDSN, "pool"); err != nil {
			logger.Error("pool migrations failed", "err", err)
			return fmt.Errorf("pool migrations: %w", err)
		}
		dialCtx, dialCancel := context.WithTimeout(ctx, pgConnectTimeout)
		defer dialCancel()
		pgPool, err := pkgstore.NewPGPool(dialCtx, pkgstore.PGConfig{DSN: pgDSN})
		if err != nil {
			return fmt.Errorf("connect PG: %w", err)
		}
		pg, err := pkgpool.NewPgRepo(pgPool)
		if err != nil {
			return fmt.Errorf("init PG repo: %w", err)
		}
		repo = pg
		logger.Info("using PG repo")
	}

	psvc := pkgpool.NewService(repo, pkgpool.Options{})

	// ---- LeaseRegistry 选择 ----
	var (
		rdb     *redis.Client
		adapter *gateway.PoolGRPCService
	)
	if leaseRedis == "" {
		logger.Info("lease registry: in-process")
		adapter = gateway.NewPoolGRPCService(psvc)
	} else {
		cli, rerr := pkgstore.NewRedisClient(ctx, pkgstore.RedisConfig{
			Addr:     leaseRedis,
			Password: leaseRedisPwd,
		})
		if rerr != nil {
			logger.Error("lease redis connect failed", "addr", leaseRedis,
				"has_password", leaseRedisPwd != "", "err", rerr)
			return fmt.Errorf("pool service: lease redis: %w", rerr)
		}
		rdb = cli
		logger.Info("lease redis connected", "addr", leaseRedis, "auth", leaseRedisPwd != "")
		reg := gateway.NewRedisLeaseRegistry(rdb, gateway.RedisLeaseRegistryOptions{})
		logger.Info("lease registry: redis", "addr", leaseRedis)
		adapter = gateway.NewPoolGRPCServiceWithRegistry(psvc, reg)
	}
	defer adapter.Close()
	defer func() {
		if rdb != nil {
			_ = rdb.Close()
		}
	}()

	// ---- mTLS ----
	var mtls *observability.MTLSConfig
	if cert := l.GetString("tls-cert"); cert != "" {
		mtls = &observability.MTLSConfig{
			CertFile:     cert,
			KeyFile:      l.GetString("tls-key"),
			ClientCAFile: l.GetString("tls-client-ca"),
		}
	}

	if err := gateway.RunStandalone(ctx, gateway.StandaloneConfig{
		GRPCAddr: grpcAddr,
		MTLS:     mtls,
		Logger:   logger,
		Register: func(s *grpc.Server) {
			poolv1.RegisterPoolServiceServer(s, adapter)
		},
	}); err != nil {
		return fmt.Errorf("pool service: %w", err)
	}
	logger.Info("pool service shutdown complete")
	return nil
}
