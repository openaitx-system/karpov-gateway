// Package music 是 Music Service 的 wiring runner。
//
// **配置优先级**：CLI flag > MGW_MUSIC_<KEY> > MGW_<KEY> > legacy env > default
package music

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	"github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	musicv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/music/v1"
	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/music"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusicprovider"
)

// Version 由 ldflags 注入。
var Version = "v0.3.0-dev"

// Run 启动 Music Service；阻塞到 ctx 取消。
func Run(ctx context.Context, args []string) error {
	l := cmdpkg.NewLoader("music")
	l.String("grpc", ":9002", "gRPC listen address")
	l.Int("max-retry", 3, "Pool acquire retries on failure")
	l.String("pool-grpc", "", "remote pool gRPC addr (empty = in-memory pool)")
	l.String("tls-cert", "", "PEM cert (mTLS if set)")
	l.String("tls-key", "", "PEM key")
	l.String("tls-client-ca", "", "PEM client CA bundle")
	l.Bool("version", false, "print version and exit")
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
	poolGRPC := l.GetString("pool-grpc")
	maxRetry := l.GetInt("max-retry")

	logger := observability.NewLogger("music-service", slog.LevelInfo)
	logger.Info("music service starting", "version", Version, "grpc", grpcAddr, "pool", poolGRPC)

	// ---- Provider Registry ----
	reg := provider.NewRegistry()
	if err := qqmusicprovider.Register(reg, qqmusic.NewClient(qqmusic.ClientOptions{})); err != nil {
		return fmt.Errorf("register qqmusic provider: %w", err)
	}

	// ---- Pool ----
	var (
		poolAcq music.PoolAcquirer
		conn    *grpc.ClientConn
	)
	if poolGRPC == "" {
		poolAcq = pool.NewService(pool.NewMemRepo(), pool.Options{})
		logger.Info("pool: in-memory")
	} else {
		c, err := grpc.NewClient(poolGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial pool service: %w", err)
		}
		conn = c
		poolAcq = pool.NewRemoteClient(poolv1.NewPoolServiceClient(conn))
		logger.Info("pool: remote gRPC", "addr", poolGRPC)
	}
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	// ---- Music Service ----
	musicSvc := music.NewService(reg, poolAcq, maxRetry)

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
			musicv1.RegisterMusicServiceServer(s, gateway.NewMusicGRPCService(musicSvc))
		},
	}); err != nil {
		return fmt.Errorf("music service: %w", err)
	}
	logger.Info("music service shutdown complete")
	return nil
}
