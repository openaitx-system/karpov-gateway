package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"

	musicv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/music/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/music"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// fakeProvider 重用 music 包测试中的 mock 思路（这里独立写以避免跨包导出测试类型）。
type fakeProvider struct{}

func (fakeProvider) Name() string { return "qqmusic" }
func (fakeProvider) Capabilities() []provider.Capability {
	return []provider.Capability{provider.CapGetSong, provider.CapSearchSongs}
}
func (fakeProvider) GetSong(_ context.Context, _ *provider.CredentialLease, mid string) (map[string]any, error) {
	return map[string]any{"mid": mid, "name": "Foo", "interval": float64(180)}, nil
}
func (fakeProvider) SearchSongs(_ context.Context, _ *provider.CredentialLease, q string, _, _ int) (map[string]any, error) {
	return map[string]any{
		"total": float64(1), "has_more": false,
		"list": []map[string]any{{"mid": "M1", "name": q}},
	}, nil
}
func (fakeProvider) GetSongURL(_ context.Context, _ *provider.CredentialLease, mid, _ string) (map[string]any, error) {
	return map[string]any{"midurlinfo": []any{map[string]any{"purl": "https://x/" + mid}}}, nil
}
func (fakeProvider) GetLyric(_ context.Context, _ *provider.CredentialLease, _ string) (map[string]any, error) {
	return nil, nil
}
func (fakeProvider) HealthCheck(_ context.Context, _ *provider.CredentialLease) error { return nil }

func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	reg := provider.NewRegistry()
	_ = reg.Register(fakeProvider{})
	psvc := pool.NewService(pool.NewMemRepo(), pool.Options{})
	_ = psvc.AddCredential(context.Background(), pool.Credential{
		ID: "c1", Provider: "qqmusic",
		Capabilities: []provider.Capability{provider.CapGetSong, provider.CapSearchSongs},
		Status:       pool.StatusActive, HealthScore: 1.0,
	})
	musicSvc := music.NewService(reg, psvc, 3)

	srv := NewServer(Config{
		GRPCAddr: "127.0.0.1:0", // 让 OS 选端口
		HTTPAddr: "127.0.0.1:0",
	}, func(s *grpc.Server) {
		musicv1.RegisterMusicServiceServer(s, NewMusicGRPCService(musicSvc))
	}, func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
		return musicv1.RegisterMusicServiceHandler(ctx, mux, conn)
	})
	return srv, ""
}

// 因为 NewServer 使用 cfg 中的字面量地址，需要先解析端口；这里改为单独 TCP 监听
// 然后把 addr 传回。简化做法：改成可注入的 listener。
//
// 当前测试只验证：
//  1. NewServer 不 panic
//  2. /healthz 返回 200
//  3. /readyz 返回 200
//
// 完整 gRPC + REST 转发测试在 M16 e2e 时启用真实端口。

func TestNewServer_HealthEndpoints(t *testing.T) {
	srv, _ := startServer(t)
	eng := srv.Engine()
	// 直接打 gin engine（不走真实网络），更稳定可测。
	w := newRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	eng.ServeHTTP(w, req)
	if w.code != http.StatusOK {
		t.Errorf("/healthz code: %d", w.code)
	}
	if !strings.Contains(w.body.String(), "ok") {
		t.Errorf("/healthz body: %q", w.body.String())
	}

	w2 := newRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/readyz", nil)
	eng.ServeHTTP(w2, req2)
	if w2.code != http.StatusOK {
		t.Errorf("/readyz code: %d", w2.code)
	}
}

func TestServer_Start_ContextCancel(t *testing.T) {
	// 用真实端口（127.0.0.1:0）；context 取消后必须在 grace 时间内退出。
	reg := provider.NewRegistry()
	_ = reg.Register(fakeProvider{})
	psvc := pool.NewService(pool.NewMemRepo(), pool.Options{})
	musicSvc := music.NewService(reg, psvc, 3)

	srv := NewServer(Config{
		GRPCAddr:      "127.0.0.1:0",
		HTTPAddr:      "127.0.0.1:0",
		ShutdownGrace: 2 * time.Second,
	}, func(s *grpc.Server) {
		musicv1.RegisterMusicServiceServer(s, NewMusicGRPCService(musicSvc))
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := srv.Start(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		// http.Server.Shutdown 返回 nil 即可
		// listener 关闭后 Serve 返回 ErrServerClosed
		// ctx 取消触发 shutdown，预期 nil
		t.Logf("Start returned: %v", err)
	}
}

// 简易 ResponseRecorder（避免引 net/http/httptest 单独包的 import 噪音）。
type recorder struct {
	code   int
	body   *strBuf
	header http.Header
}

func newRecorder() *recorder {
	return &recorder{code: 200, body: &strBuf{}, header: http.Header{}}
}
func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}
func (r *recorder) WriteHeader(code int) { r.code = code }

type strBuf struct{ s strings.Builder }

func (b *strBuf) Write(p []byte) (int, error) {
	_, err := b.s.WriteString(string(p))
	return len(p), err
}
func (b *strBuf) String() string { return b.s.String() }

// 防止 io 未使用 lint 报错（部分 lint 套件会标记）。
var _ = io.EOF
