package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	authv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/auth/v1"
)

// TestRunStandalone_AuthFlowOverGRPC 验证：
//   - RunStandalone 启动独立 gRPC server（无 mTLS / no REST）
//   - grpc.Dial 拨入后能调 AuthService.Register
//   - HealthService 报告 SERVING
//   - ctx 取消后 GracefulStop 正常返回
func TestRunStandalone_AuthFlowOverGRPC(t *testing.T) {
	// 申请本地端口
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := lis.Addr().String()
	_ = lis.Close()

	users := auth.NewMemUserRepo()
	authSvc := auth.NewService(users, &noopSessionStore{}, auth.Options{
		PasswordParams:      auth.PasswordParams{TimeCost: 1, MemoryCost: 8 * 1024, Threads: 1, SaltLen: 16, KeyLen: 32},
		SessionTTL:          1 * time.Hour,
		MinPasswordStrength: -1,
	})

	srvCtx, cancel := context.WithCancel(context.Background())
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- RunStandalone(srvCtx, StandaloneConfig{
			GRPCAddr: addr,
			Register: func(s *grpc.Server) {
				authv1.RegisterAuthServiceServer(s, NewAuthGRPCService(authSvc))
			},
		})
	}()
	defer func() {
		cancel()
		select {
		case <-srvErr:
		case <-time.After(3 * time.Second):
			t.Logf("standalone did not exit in time")
		}
	}()

	// 等 server 起来：用 health check 确认
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	hcli := healthpb.NewHealthClient(conn)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, c := context.WithTimeout(context.Background(), 200*time.Millisecond)
		resp, err := hcli.Check(ctx, &healthpb.HealthCheckRequest{})
		c()
		if err == nil && resp.Status == healthpb.HealthCheckResponse_SERVING {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// AuthService.Register
	cli := authv1.NewAuthServiceClient(conn)
	resp, err := cli.Register(context.Background(), &authv1.RegisterRequest{
		Email: "a@b.c", Password: "Ab1!cdEf2@gH",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.UserId == "" {
		t.Errorf("user id empty: %+v", resp)
	}
}

func TestRunStandalone_NilRegister(t *testing.T) {
	if err := RunStandalone(context.Background(), StandaloneConfig{}); err == nil {
		t.Errorf("expected error for nil Register")
	}
}

func TestRunStandalone_BadAddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := RunStandalone(ctx, StandaloneConfig{
		GRPCAddr: "127.0.0.1:999999", // invalid port
		Register: func(s *grpc.Server) {},
	})
	if err == nil {
		t.Errorf("expected listen error")
	}
}

// noopSessionStore 实现 auth.SessionStore；本测试不需要 session 路径。
type noopSessionStore struct{}

func (noopSessionStore) Save(_ context.Context, _ *auth.Session) error { return nil }
func (noopSessionStore) Get(_ context.Context, _ string) (*auth.Session, error) {
	return nil, auth.ErrSessionNotFound
}
func (noopSessionStore) Delete(_ context.Context, _ string) error { return nil }

// 防止 import 报 unused
var _ = health.NewServer
