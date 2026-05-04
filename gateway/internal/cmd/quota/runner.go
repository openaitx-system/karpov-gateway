// Package quota 是 Quota Service 的 wiring runner（M39 接入真实业务）。
//
// **配置优先级**：CLI flag > MGW_QUOTA_<KEY> > MGW_<KEY> > legacy env > default
//
// 架构关系：
//   - 业务实现：internal/quota.Service（Redis Lua 双层窗口）
//   - gRPC adapter：internal/gateway.QuotaGRPCService
//   - 默认规则：internal/gateway.MemRuleStore（free 1000/d, 30000/月, 80% 软限）
package quota

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/grpc"

	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	"github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	quotav1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/quota/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
	"github.com/MiChongs/QQMusicApi/gateway/internal/quota"
	"github.com/MiChongs/QQMusicApi/gateway/internal/store"
)

// Version 由 ldflags 注入。
var Version = "v0.3.0-dev"

// Run 启动 Quota Service。
func Run(ctx context.Context, args []string) error {
	l := cmdpkg.NewLoader("quota")
	l.String("grpc", ":9004", "gRPC listen address")
	l.String("redis", "127.0.0.1:6379", "Redis address (sliding window counters)")
	l.String("redis-password", "", "Redis password")
	l.String("tls-cert", "", "PEM cert (mTLS if set)")
	l.String("tls-key", "", "PEM key")
	l.String("tls-client-ca", "", "PEM client CA bundle")
	l.Bool("version", false, "print version and exit")
	l.LegacyEnv("redis-password", "REDIS_PASSWORD")
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
	redisAddr := cmdpkg.ComposeRedisAddr(l.GetString("redis"))
	redisPassword := l.GetString("redis-password")
	if redisPassword == "" {
		redisPassword = os.Getenv("REDIS_PASSWORD")
	}

	logger := observability.NewLogger("quota-service", slog.LevelInfo)
	logger.Info("quota service starting", "version", Version, "grpc", grpcAddr, "redis", redisAddr)

	// ---- Redis ----
	rdb, err := store.NewRedisClient(ctx, store.RedisConfig{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	if err != nil {
		logger.Error("redis connect failed", "addr", redisAddr,
			"has_password", redisPassword != "", "err", err)
		return fmt.Errorf("quota service: %w", err)
	}
	defer func() { _ = rdb.Close() }()
	logger.Info("redis connected", "addr", redisAddr, "auth", redisPassword != "")

	// ---- 业务 + 默认规则 ----
	svc := quota.NewService(rdb, quota.Options{})
	rules := gateway.NewMemRuleStore()

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
			quotav1.RegisterQuotaServiceServer(s, gateway.NewQuotaGRPCService(svc, rules))
		},
	}); err != nil {
		return fmt.Errorf("quota service: %w", err)
	}
	logger.Info("quota service shutdown complete")
	return nil
}
