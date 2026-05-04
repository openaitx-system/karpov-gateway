package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
)

// StandaloneConfig 是独立 gRPC service 启动配置（cmd/auth / cmd/music / cmd/pool）。
//
// 与 Edge Gateway 同进程模式（NewServer）不同，Standalone 只起 gRPC server，
// 不开 REST。HTTP/REST 由 Edge Gateway 远程 dial 这些服务后由 grpc-gateway 反射。
//
// 默认开 grpc-health（k8s liveness/readiness 用）+ reflection（grpcurl 调试）。
type StandaloneConfig struct {
	GRPCAddr      string                    // 默认 ":9000"
	ShutdownGrace time.Duration             // 默认 15s
	MTLS          *observability.MTLSConfig // nil = insecure
	Register      func(s *grpc.Server)      // 业务 service 注册回调
	Logger        *slog.Logger              // nil = slog.Default
	HealthService string                    // 健康检查注册 service name；默认 ""（整体）
}

// RunStandalone 启动一个独立 gRPC service 直到 ctx 取消。
//
// MTLS 非 nil 时强制双向 TLS（RequireAndVerifyClientCert）；
// 否则用 insecure（仅本机回环 / VPC 内网场景）。
func RunStandalone(ctx context.Context, cfg StandaloneConfig) error {
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9000"
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 15 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Register == nil {
		return errors.New("gateway.RunStandalone: nil Register")
	}

	creds, err := buildServerCreds(cfg.MTLS)
	if err != nil {
		return fmt.Errorf("gateway.RunStandalone: creds: %w", err)
	}

	s := grpc.NewServer(grpc.Creds(creds))
	cfg.Register(s)

	// 健康检查 + 反射
	hsrv := health.NewServer()
	hsrv.SetServingStatus(cfg.HealthService, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(s, hsrv)
	reflection.Register(s)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("gateway.RunStandalone: listen: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("standalone gRPC ready", "addr", cfg.GRPCAddr, "mtls", cfg.MTLS != nil)
		if err := s.Serve(lis); err != nil {
			errCh <- fmt.Errorf("serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
		defer cancel()
		done := make(chan struct{})
		go func() {
			s.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
			return nil
		case <-stopCtx.Done():
			s.Stop()
			return stopCtx.Err()
		}
	case err := <-errCh:
		s.Stop()
		return err
	}
}

// buildServerCreds 根据 MTLSConfig 决定 transport credentials。
func buildServerCreds(cfg *observability.MTLSConfig) (credentials.TransportCredentials, error) {
	if cfg == nil {
		return insecure.NewCredentials(), nil
	}
	tlsCfg, err := observability.LoadServerTLSConfig(*cfg)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(tlsCfg), nil
}
