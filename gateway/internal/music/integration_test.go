package music

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
	"github.com/MiChongs/QQMusicApi/gateway/internal/quota"
)

// TestIntegration_AuthQuotaPoolMusic 跑一遍 walking-skeleton：
//
//	Register/Login → 拿 Session → Quota.CheckAndConsume → Music.GetSong → Quota Refund (失败时)
//
// 不接 PG（in-memory repo + miniredis），证明 4 个 service 的接口与状态机互相
// 自洽。这是 M19 v0.1 e2e 测试的最小子集。
func TestIntegration_AuthQuotaPoolMusic(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// ---- Auth 装配 ----
	users := auth.NewMemUserRepo()
	sessions := auth.NewRedisSessionStore(rdb, "")
	authSvc := auth.NewService(users, sessions, auth.Options{
		PasswordParams:      auth.PasswordParams{TimeCost: 1, MemoryCost: 8 * 1024, Threads: 1, SaltLen: 16, KeyLen: 32},
		SessionTTL:          1 * time.Hour,
		MinPasswordStrength: -1, // 集成测试模拟黑盒；密码强度评分单测在 auth/strength_test.go
	})

	// 1) 用户注册 + 登录
	u, err := authSvc.Register(ctx, "alice@example.com", "P@ssw0rd!", "", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	res, err := authSvc.Login(ctx, "alice@example.com", "P@ssw0rd!", "", "127.0.0.1", "test-ua")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	sess := res.Session
	if sess == nil || sess.UserID != u.ID {
		t.Fatalf("session user mismatch")
	}

	// ---- Quota 装配 ----
	quotaSvc := quota.NewService(rdb, quota.Options{})

	// ---- Pool + Music 装配 ----
	repo := pool.NewMemRepo()
	psvc := pool.NewService(repo, pool.Options{})
	_ = psvc.AddCredential(ctx, pool.Credential{
		ID:           "qq-1",
		Provider:     "qqmusic",
		Capabilities: []provider.Capability{provider.CapGetSong, provider.CapSearchSongs},
		Status:       pool.StatusActive,
		HealthScore:  1.0,
	})
	reg := provider.NewRegistry()
	fp := &fakeProvider{
		name: "qqmusic",
		songResp: map[string]any{
			"mid": "M_e2e", "name": "E2E Song", "interval": float64(200),
		},
	}
	_ = reg.Register(fp)
	musicSvc := NewService(reg, psvc, 3)

	// 2) Quota 扣减（GetSong 权重=1）
	rules := quota.Rules{
		UserID: u.ID, Provider: "qqmusic", Endpoint: "GetSong",
		Weight: 1, DayLimit: 1000, MonthLimit: 10000, SoftLimitPct: 80,
	}
	dec, err := quotaSvc.CheckAndConsume(ctx, rules)
	if err != nil {
		t.Fatalf("quota: %v", err)
	}
	if dec.Decision != quota.DecisionAllow {
		t.Fatalf("expected Allow, got %v", dec.Decision)
	}

	// 3) Music 调用
	song, err := musicSvc.GetSong(ctx, "qqmusic", "M_e2e")
	if err != nil {
		t.Fatalf("music: %v", err)
	}
	if song.MID != "M_e2e" || song.Name != "E2E Song" {
		t.Errorf("song: %+v", song)
	}

	// 4) Session 验证
	verifiedUser, _, err := authSvc.VerifySession(ctx, sess.SID)
	if err != nil || verifiedUser.ID != u.ID {
		t.Fatalf("verify session: %v", err)
	}

	// 5) Pool 健康检查应当看到 1 个 active
	hs, _ := psvc.HealthSummary(ctx, "qqmusic")
	if hs.ActiveCount != 1 || hs.AverageScore < 1.0 {
		t.Errorf("health: %+v", hs)
	}

	// 6) Quota usage 查询应当看到 day=1 month=1
	usage, err := quotaSvc.GetUsage(ctx, rules)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.DayUsed != 1 || usage.MonthUsed != 1 {
		t.Errorf("usage: day=%d month=%d", usage.DayUsed, usage.MonthUsed)
	}

	// 7) Logout
	if err := authSvc.Logout(ctx, sess.SID); err != nil {
		t.Fatalf("logout: %v", err)
	}
}
