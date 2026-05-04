package pool_test

// remoteclient_test.go 在 _test 子包跑：避免 internal 包自循环引用。
// 通过 bufconn 起一个真实 gRPC server（挂 PoolGRPCService）→ 拿
// RemoteClient → 走 Acquire/Release 全程验证 wire round-trip。

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// startBufServer 起 bufconn server，挂上 PoolGRPCService。
func startBufServer(t *testing.T) (*pool.Service, *grpc.ClientConn, func()) {
	t.Helper()

	repo := pool.NewMemRepo()
	svc := pool.NewService(repo, pool.Options{})
	adapter := gateway.NewPoolGRPCService(svc)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	poolv1.RegisterPoolServiceServer(gs, adapter)
	go func() { _ = gs.Serve(lis) }()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		gs.Stop()
		_ = lis.Close()
		adapter.Close()
	}
	return svc, conn, cleanup
}

func TestRemoteClient_AcquireRelease_RoundTrip(t *testing.T) {
	svc, conn, cleanup := startBufServer(t)
	defer cleanup()

	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: "c1", Provider: "qqmusic", Payload: []byte("payload-bytes"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: 0.9,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cli := pool.NewRemoteClient(poolv1.NewPoolServiceClient(conn))

	lease, err := cli.Acquire(context.Background(), "qqmusic",
		pool.AcquireOptions{Capability: provider.CapGetSong})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if lease.ID != "c1" || string(lease.Payload) != "payload-bytes" {
		t.Errorf("lease: %+v", lease)
	}

	// Release OK：服务端 health 应升 0.01。
	lease.Release(provider.PoolResultOK)

	// 给 RPC 一点时间到达服务端（Release 是 fire-and-forget）。
	time.Sleep(80 * time.Millisecond)

	sum, err := svc.HealthSummary(context.Background(), "qqmusic")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	// 0.9 + 0.01 = 0.91（浮点容差）。
	if sum.AverageScore < 0.905 || sum.AverageScore > 0.915 {
		t.Errorf("health: %v want ~0.91", sum.AverageScore)
	}
}

func TestRemoteClient_Acquire_NoCandidate(t *testing.T) {
	_, conn, cleanup := startBufServer(t)
	defer cleanup()

	cli := pool.NewRemoteClient(poolv1.NewPoolServiceClient(conn))
	_, err := cli.Acquire(context.Background(), "qqmusic",
		pool.AcquireOptions{Capability: provider.CapGetSong})
	if !errors.Is(err, pool.ErrNoCandidate) {
		t.Errorf("expected ErrNoCandidate, got %v", err)
	}
}

func TestRemoteClient_Release_AuthFailed_BansCredential(t *testing.T) {
	svc, conn, cleanup := startBufServer(t)
	defer cleanup()

	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: "c1", Provider: "qqmusic", Payload: []byte("p"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: 1.0,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cli := pool.NewRemoteClient(poolv1.NewPoolServiceClient(conn))

	lease, err := cli.Acquire(context.Background(), "qqmusic",
		pool.AcquireOptions{Capability: provider.CapGetSong})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.Release(provider.PoolResultAuthFailed)
	time.Sleep(80 * time.Millisecond)

	sum, _ := svc.HealthSummary(context.Background(), "qqmusic")
	if sum.BannedCount != 1 {
		t.Errorf("expected banned=1, got %d", sum.BannedCount)
	}
}
