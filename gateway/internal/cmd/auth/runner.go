// Package auth 是 Auth Service 的 wiring runner。
//
// 见 internal/cmd 包注释；cmd/auth/main.go 与 cmd/qqmusic-gateway 都通过 Run
// 调用本文件，避免双份装配。
//
// **配置优先级**（高 → 低）：
//
//	CLI flag → MGW_AUTH_<KEY> env → MGW_<KEY> env → legacy env (REDIS_PASSWORD…) → default
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"google.golang.org/grpc"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	"github.com/MiChongs/QQMusicApi/gateway/internal/auth/hibp"
	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	"github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	authv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/auth/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
	"github.com/MiChongs/QQMusicApi/gateway/internal/store"
)

// Version 由 ldflags 注入：-ldflags '-X .../internal/cmd/auth.Version=...'
var Version = "v0.3.0-dev"

// Run 启动 Auth Service；阻塞到 ctx 取消。
func Run(ctx context.Context, args []string) error {
	l := cmdpkg.NewLoader("auth")
	l.String("grpc", ":9001", "gRPC listen address")
	l.String("redis", "127.0.0.1:6379", "Redis address (sessions)")
	l.String("redis-password", "", "Redis password")
	l.Duration("session-ttl", 7*24*time.Hour, "session TTL")
	l.Bool("hibp", true, "enable HIBP password breach lookup on Register")
	l.String("bootstrap-email", "admin@example.com", "email for first-run superadmin auto-bootstrap")
	l.Bool("bootstrap-disable", false, "disable first-run superadmin auto-bootstrap")
	l.String("tls-cert", "", "PEM cert (enable mTLS if set)")
	l.String("tls-key", "", "PEM key")
	l.String("tls-client-ca", "", "PEM client CA bundle (mTLS)")
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
	sessionTTL := l.GetDuration("session-ttl")

	logger := observability.NewLogger("auth-service", slog.LevelInfo)
	logger.Info("auth service starting", "version", Version, "grpc", grpcAddr)

	// ---- 依赖装配 ----
	rdb, err := store.NewRedisClient(ctx, store.RedisConfig{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	if err != nil {
		logger.Error("redis connect failed", "addr", redisAddr,
			"has_password", redisPassword != "", "err", err)
		return fmt.Errorf("auth service: %w", err)
	}
	defer func() { _ = rdb.Close() }()
	logger.Info("redis connected", "addr", redisAddr, "auth", redisPassword != "")

	users := auth.NewMemUserRepo() // v0.3 后续接 PG（与 pool 同套件）
	sessions := auth.NewRedisSessionStore(rdb, "")
	apiKeys := auth.NewMemAPIKeyRepo()

	opts := auth.Options{
		PasswordParams:     auth.DefaultPasswordParams(),
		SessionTTL:         sessionTTL,
		APIKeyRepo:         apiKeys,
		Logger:             logger,
		TOTPIssuer:         "Karpov",
		TOTPPendingStore:   auth.NewRedisPendingTOTPStore(rdb, ""),
		TOTPChallengeStore: auth.NewRedisTOTPChallengeStore(rdb, ""),
		TOTPReplayBlocker:  auth.NewTOTPReplayBlocker(rdb, ""),
	}
	if l.GetBool("hibp") {
		opts.PwnedChecker = hibp.NewClient("", nil)
	}
	authSvc := auth.NewService(users, sessions, opts)

	// ---- 首次启动自动创建 superadmin（幂等）----
	bootRes, bootErr := authSvc.Bootstrap(ctx, auth.BootstrapOptions{
		Email:    l.GetString("bootstrap-email"),
		Disabled: l.GetBool("bootstrap-disable"),
		Logger:   logger,
	})
	if bootErr != nil && !errors.Is(bootErr, auth.ErrBootstrapDisabled) {
		logger.Error("auth bootstrap failed", "err", bootErr)
	}
	auth.PrintBootstrapBanner(os.Stderr, bootRes)

	// ---- mTLS 配置 ----
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
			authv1.RegisterAuthServiceServer(s, gateway.NewAuthGRPCService(authSvc))
		},
	}); err != nil {
		return fmt.Errorf("auth service: %w", err)
	}
	logger.Info("auth service shutdown complete")
	return nil
}
