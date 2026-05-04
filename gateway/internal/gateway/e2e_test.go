package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	authv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/auth/v1"
	musicv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/music/v1"
	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/music"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// TestE2E_FullStack 真实启动 gRPC + HTTP，跑过完整 Auth + Music 链路。
//
// 步骤：
//  1. /healthz 200
//  2. POST /v1/auth/register
//  3. POST /v1/auth/login → 拿 sid
//  4. GET /v1/auth/me（带 cookie sid）→ 看到 user
//  5. GET /v1/qqmusic/songs/M_demo（无凭据 → 503）
//  6. POST /v1/auth/logout（带 sid）→ 200
//  7. GET /v1/auth/me（再次）→ 401
func TestE2E_FullStack(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e skipped in short mode")
	}

	// 申请两个本地端口
	grpcLis, _ := net.Listen("tcp", "127.0.0.1:0")
	grpcAddr := grpcLis.Addr().String()
	_ = grpcLis.Close()
	httpLis, _ := net.Listen("tcp", "127.0.0.1:0")
	httpAddr := httpLis.Addr().String()
	_ = httpLis.Close()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// Auth：测试用低成本 argon2 + 短 TTL
	users := auth.NewMemUserRepo()
	sessions := auth.NewRedisSessionStore(rdb, "")
	authSvc := auth.NewService(users, sessions, auth.Options{
		PasswordParams:      auth.PasswordParams{TimeCost: 1, MemoryCost: 8 * 1024, Threads: 1, SaltLen: 16, KeyLen: 32},
		SessionTTL:          1 * time.Hour,
		MinPasswordStrength: -1, // e2e 黑盒测网关链路；密码强度评分单测在 auth/strength_test.go
	})

	// Music：fakeProvider 返回固定数据
	reg := provider.NewRegistry()
	_ = reg.Register(fakeProvider{})
	psvc := pool.NewService(pool.NewMemRepo(), pool.Options{})
	_ = psvc.AddCredential(context.Background(), pool.Credential{
		ID: "c1", Provider: "qqmusic",
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: 1.0,
	})
	musicSvc := music.NewService(reg, psvc, 3)

	srv := NewServer(Config{
		GRPCAddr:      grpcAddr,
		HTTPAddr:      httpAddr,
		ShutdownGrace: 2 * time.Second,
		DisableCSRF:   true, // e2e 直跑 REST，不模拟浏览器 CSRF 流；CSRF 单测在 security_test.go
		// admin 路由保护：测试用 secret-admin
		AdminAuth: &AdminAuthOptions{AdminTokens: []string{"secret-admin"}},
	}, func(s *grpc.Server) {
		authv1.RegisterAuthServiceServer(s, NewAuthGRPCService(authSvc))
		musicv1.RegisterMusicServiceServer(s, NewMusicGRPCService(musicSvc))
		poolv1.RegisterPoolServiceServer(s, NewPoolGRPCService(psvc))
	}, func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
		if err := authv1.RegisterAuthServiceHandler(ctx, mux, conn); err != nil {
			return err
		}
		if err := musicv1.RegisterMusicServiceHandler(ctx, mux, conn); err != nil {
			return err
		}
		return poolv1.RegisterPoolServiceHandler(ctx, mux, conn)
	})

	srvCtx, cancel := context.WithCancel(context.Background())
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- srv.Start(srvCtx)
	}()
	defer func() {
		cancel()
		select {
		case <-srvErr:
		case <-time.After(3 * time.Second):
			t.Logf("server did not exit in time")
		}
	}()

	// 等 server 起来
	httpURL := "http://" + httpAddr
	if !waitHTTPReady(t, httpURL+"/healthz", 2*time.Second) {
		t.Fatalf("server not ready")
	}

	cli := &http.Client{Timeout: 3 * time.Second}

	// 1) /healthz
	mustGetStatus(t, cli, httpURL+"/healthz", 200)

	// 2) register
	regBody := `{"email":"a@b.c","password":"hunter22"}`
	resp := mustPostJSON(t, cli, httpURL+"/v1/auth/register", regBody)
	var regOut map[string]any
	_ = json.Unmarshal(resp, &regOut)
	if regOut["userId"] == nil && regOut["user_id"] == nil {
		t.Errorf("register response missing user_id: %s", string(resp))
	}

	// 3) login → 拿 sid
	loginBody := `{"email":"a@b.c","password":"hunter22"}`
	resp = mustPostJSON(t, cli, httpURL+"/v1/auth/login", loginBody)
	var loginOut map[string]any
	_ = json.Unmarshal(resp, &loginOut)
	sid, _ := loginOut["sid"].(string)
	if sid == "" {
		t.Fatalf("login no sid: %s", string(resp))
	}

	// 4) /v1/auth/me with cookie
	req, _ := http.NewRequest("GET", httpURL+"/v1/auth/me", nil)
	req.Header.Set("Cookie", "sid="+sid)
	mr2, err := cli.Do(req)
	if err != nil {
		t.Fatalf("/me: %v", err)
	}
	body, _ := io.ReadAll(mr2.Body)
	mr2.Body.Close()
	if mr2.StatusCode != 200 {
		t.Errorf("/me status=%d body=%s", mr2.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "a@b.c") {
		t.Errorf("/me body missing email: %s", string(body))
	}

	// 5) /v1/qqmusic/songs/M_demo → 200（fakeProvider returns success）
	r5, err := cli.Get(httpURL + "/v1/qqmusic/songs/M_demo")
	if err != nil {
		t.Fatalf("/songs: %v", err)
	}
	b5, _ := io.ReadAll(r5.Body)
	r5.Body.Close()
	if r5.StatusCode != 200 {
		t.Errorf("/songs status=%d body=%s", r5.StatusCode, string(b5))
	}
	var songOut map[string]any
	_ = json.Unmarshal(b5, &songOut)
	if songOut["title"] != "Foo" {
		t.Errorf("song title: %v (full body=%s)", songOut["title"], string(b5))
	}

	// 6) logout
	logoutBody := `{"sid":"` + sid + `"}`
	r6, err := cli.Post(httpURL+"/v1/auth/logout", "application/json", strings.NewReader(logoutBody))
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	b6, _ := io.ReadAll(r6.Body)
	r6.Body.Close()
	if r6.StatusCode != 200 {
		t.Errorf("logout status=%d body=%s", r6.StatusCode, string(b6))
	}

	// 7) /me 再访问应当 401
	req7, _ := http.NewRequest("GET", httpURL+"/v1/auth/me", nil)
	req7.Header.Set("Cookie", "sid="+sid)
	r7, _ := cli.Do(req7)
	if r7.StatusCode != 401 {
		t.Errorf("/me after logout status=%d", r7.StatusCode)
	}
	r7.Body.Close()

	// 8a) admin 无 token → 401
	r8a, _ := cli.Get(httpURL + "/v1/admin/pool/health/qqmusic")
	if r8a.StatusCode != 401 {
		t.Errorf("admin without token: %d, want 401", r8a.StatusCode)
	}
	r8a.Body.Close()

	// 8b) admin 错 token → 403
	req8b, _ := http.NewRequest("GET", httpURL+"/v1/admin/pool/health/qqmusic", nil)
	req8b.Header.Set("X-Admin-Key", "wrong")
	r8b, _ := cli.Do(req8b)
	if r8b.StatusCode != 403 {
		t.Errorf("admin wrong token: %d, want 403", r8b.StatusCode)
	}
	r8b.Body.Close()

	// 8) Pool admin REST：POST /v1/admin/pool/credentials 加凭据（带正确 token）
	addBody := `{"provider":"qqmusic","label":"e2e","payload":"` +
		// 任意 base64（grpc-gateway 把 bytes 字段按 base64 解；这里用 'aGVsbG8=' = "hello"）
		"aGVsbG8=" + `","capabilities":["GetSong"]}`
	req8, _ := http.NewRequest("POST", httpURL+"/v1/admin/pool/credentials", strings.NewReader(addBody))
	req8.Header.Set("Content-Type", "application/json")
	req8.Header.Set("X-Admin-Key", "secret-admin")
	r8, err := cli.Do(req8)
	if err != nil {
		t.Fatalf("admin add: %v", err)
	}
	b8, _ := io.ReadAll(r8.Body)
	r8.Body.Close()
	if r8.StatusCode != 200 {
		t.Errorf("admin add status=%d body=%s", r8.StatusCode, string(b8))
	}
	var addOut map[string]any
	_ = json.Unmarshal(b8, &addOut)
	addedID, _ := addOut["id"].(string)
	if addedID == "" {
		t.Errorf("admin add missing id: %s", string(b8))
	}

	// 9) GET /v1/admin/pool/health/qqmusic（带 admin token）
	req9, _ := http.NewRequest("GET", httpURL+"/v1/admin/pool/health/qqmusic", nil)
	req9.Header.Set("X-Admin-Key", "secret-admin")
	r9, err := cli.Do(req9)
	if err != nil {
		t.Fatalf("admin health: %v", err)
	}
	b9, _ := io.ReadAll(r9.Body)
	r9.Body.Close()
	if r9.StatusCode != 200 {
		t.Errorf("admin health status=%d body=%s", r9.StatusCode, string(b9))
	}

	// 10) DELETE 删除刚加的凭据（带 admin token）
	if addedID != "" {
		req10, _ := http.NewRequest("DELETE", httpURL+"/v1/admin/pool/credentials/"+addedID, nil)
		req10.Header.Set("X-Admin-Key", "secret-admin")
		r10, _ := cli.Do(req10)
		if r10.StatusCode != 200 {
			b10, _ := io.ReadAll(r10.Body)
			t.Errorf("admin delete status=%d body=%s", r10.StatusCode, string(b10))
		}
		r10.Body.Close()
	}
}

// ---- helpers ----

func waitHTTPReady(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	cli := &http.Client{Timeout: 200 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := cli.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func mustGetStatus(t *testing.T, cli *http.Client, url string, want int) {
	t.Helper()
	resp, err := cli.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status=%d want=%d body=%s", url, resp.StatusCode, want, string(body))
	}
}

func mustPostJSON(t *testing.T, cli *http.Client, url, body string) []byte {
	t.Helper()
	resp, err := cli.Post(url, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("POST %s status=%d body=%s", url, resp.StatusCode, string(b))
	}
	return b
}
