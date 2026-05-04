package pool

import (
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

func newTestService(t *testing.T) (*Service, *MemRepo, *time.Time) {
	t.Helper()
	repo := NewMemRepo()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	svc := NewService(repo, Options{
		Clock: clock,
		Rand:  rand.New(rand.NewPCG(42, 0xCAFE)),
	})
	return svc, repo, &now
}

func TestAcquire_NoCandidate(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.Acquire(context.Background(), "qqmusic", AcquireOptions{Capability: provider.CapGetSong})
	if err != ErrNoCandidate {
		t.Errorf("expected ErrNoCandidate, got %v", err)
	}
}

func TestAcquire_FilterByStatusAndCooldown(t *testing.T) {
	svc, _, now := newTestService(t)
	ctx := context.Background()

	must := func(c Credential) {
		if err := svc.AddCredential(ctx, c); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	must(Credential{ID: "ok", Provider: "qqmusic", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 1.0})
	must(Credential{ID: "banned", Provider: "qqmusic", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusBanned, HealthScore: 1.0})
	must(Credential{ID: "cooldown", Provider: "qqmusic", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 1.0, CooldownUntil: now.Add(5 * time.Minute)})

	for i := 0; i < 10; i++ {
		lease, err := svc.Acquire(ctx, "qqmusic", AcquireOptions{Capability: provider.CapGetSong})
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if lease.ID != "ok" {
			t.Errorf("got %s; expected only 'ok'", lease.ID)
		}
		lease.Release(provider.PoolResultOK)
	}
}

func TestRelease_OK_BoostsHealth(t *testing.T) {
	svc, repo, _ := newTestService(t)
	ctx := context.Background()
	_ = svc.AddCredential(ctx, Credential{ID: "x", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 0.5, FailCount: 3})
	lease, err := svc.Acquire(ctx, "p", AcquireOptions{Capability: provider.CapGetSong})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.Release(provider.PoolResultOK)

	got, _ := repo.Get(ctx, "x")
	if got.HealthScore != 0.51 {
		t.Errorf("health: %v", got.HealthScore)
	}
	if got.FailCount != 0 {
		t.Errorf("fail_count not reset: %d", got.FailCount)
	}
}

func TestRelease_AuthFailed_Bans(t *testing.T) {
	svc, repo, _ := newTestService(t)
	ctx := context.Background()
	_ = svc.AddCredential(ctx, Credential{ID: "x", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 1.0})
	lease, _ := svc.Acquire(ctx, "p", AcquireOptions{Capability: provider.CapGetSong})
	lease.Release(provider.PoolResultAuthFailed)

	got, _ := repo.Get(ctx, "x")
	if got.Status != StatusBanned {
		t.Errorf("status: %v", got.Status)
	}
}

func TestRelease_RateLimited_Cooldown(t *testing.T) {
	svc, repo, now := newTestService(t)
	ctx := context.Background()
	_ = svc.AddCredential(ctx, Credential{ID: "x", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 1.0})
	lease, _ := svc.Acquire(ctx, "p", AcquireOptions{Capability: provider.CapGetSong})
	lease.Release(provider.PoolResultRateLimited)

	got, _ := repo.Get(ctx, "x")
	if got.HealthScore != 0.8 {
		t.Errorf("health post-RL: %v", got.HealthScore)
	}
	want := now.Add(5 * time.Minute)
	if !got.CooldownUntil.Equal(want) {
		t.Errorf("cooldown: %v want %v", got.CooldownUntil, want)
	}
}

func TestRelease_NetworkError_Cooldown_After5(t *testing.T) {
	svc, repo, now := newTestService(t)
	ctx := context.Background()
	_ = svc.AddCredential(ctx, Credential{ID: "x", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 1.0})
	for i := 0; i < 5; i++ {
		lease, err := svc.Acquire(ctx, "p", AcquireOptions{Capability: provider.CapGetSong})
		if err != nil {
			t.Fatalf("acquire #%d: %v", i, err)
		}
		lease.Release(provider.PoolResultNetworkError)
	}
	got, _ := repo.Get(ctx, "x")
	want := now.Add(1 * time.Minute)
	if !got.CooldownUntil.Equal(want) {
		t.Errorf("cooldown after 5 NE: %v want %v", got.CooldownUntil, want)
	}
	if got.FailCount != 0 {
		t.Errorf("fail_count should reset after cooldown set: %d", got.FailCount)
	}
}

func TestHealthSummary(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_ = svc.AddCredential(ctx, Credential{ID: "a", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 0.7})
	_ = svc.AddCredential(ctx, Credential{ID: "b", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 0.5})
	_ = svc.AddCredential(ctx, Credential{ID: "c", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusBanned, HealthScore: 0.0})

	sum, err := svc.HealthSummary(ctx, "p")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sum.ActiveCount != 2 || sum.BannedCount != 1 {
		t.Errorf("counts: %+v", sum)
	}
	if sum.AverageScore != 0.6 {
		t.Errorf("avg: %v", sum.AverageScore)
	}
}

func TestAcquire_WeightedDistribution(t *testing.T) {
	// 高 health 凭据应获得更多次选中。
	// 注意：不调用 Release——避免 PoolResultOK 把 low 的 health 拉升到与 high 同水平，
	// 那会让 1000 次循环里的偏差被消解（这是真实生产的预期行为，而非 bug）。
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_ = svc.AddCredential(ctx, Credential{ID: "high", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 0.9})
	_ = svc.AddCredential(ctx, Credential{ID: "low", Provider: "p", Capabilities: []provider.Capability{provider.CapGetSong}, Status: StatusActive, HealthScore: 0.1})

	counts := map[string]int{}
	for i := 0; i < 2000; i++ {
		lease, err := svc.Acquire(ctx, "p", AcquireOptions{Capability: provider.CapGetSong})
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		counts[lease.ID]++
	}
	// 期望比 ≈ 0.9 / 0.1 = 9x；保守阈值 ≥ 5x 给 PCG 抖动留余量。
	if counts["high"] < 5*counts["low"] {
		t.Errorf("weighted bias too weak: high=%d low=%d", counts["high"], counts["low"])
	}
}

func TestListCredentials_FilterAndPaginate(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	caps := []provider.Capability{provider.CapGetSong}
	add := func(id string, st Status) {
		if err := svc.AddCredential(ctx, Credential{ID: id, Provider: "qqmusic", Capabilities: caps, Status: st, HealthScore: 1}); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	add("a", StatusActive)
	add("b", StatusActive)
	add("c", StatusDisabled)
	add("d", StatusBanned)

	t.Run("no_filter", func(t *testing.T) {
		items, total, err := svc.ListCredentials(ctx, ListCredentialsOptions{Provider: "qqmusic"})
		if err != nil {
			t.Fatal(err)
		}
		if total != 4 || len(items) != 4 {
			t.Errorf("want 4/4, got %d/%d", len(items), total)
		}
	})
	t.Run("status_filter", func(t *testing.T) {
		items, total, err := svc.ListCredentials(ctx, ListCredentialsOptions{Provider: "qqmusic", StatusFilter: "active"})
		if err != nil {
			t.Fatal(err)
		}
		if total != 2 || len(items) != 2 {
			t.Errorf("want 2/2, got %d/%d", len(items), total)
		}
		for _, it := range items {
			if it.Status != StatusActive {
				t.Errorf("status filter leaked: %v", it.Status)
			}
		}
	})
	t.Run("pagination", func(t *testing.T) {
		page1, total1, err := svc.ListCredentials(ctx, ListCredentialsOptions{Provider: "qqmusic", Limit: 2, Offset: 0})
		if err != nil {
			t.Fatal(err)
		}
		page2, total2, err := svc.ListCredentials(ctx, ListCredentialsOptions{Provider: "qqmusic", Limit: 2, Offset: 2})
		if err != nil {
			t.Fatal(err)
		}
		if total1 != 4 || total2 != 4 {
			t.Errorf("pagination total drift: %d %d", total1, total2)
		}
		if len(page1) != 2 || len(page2) != 2 {
			t.Errorf("page sizes wrong: %d %d", len(page1), len(page2))
		}
		seen := map[string]bool{}
		for _, it := range page1 {
			seen[it.ID] = true
		}
		for _, it := range page2 {
			seen[it.ID] = true
		}
		if len(seen) != 4 {
			t.Errorf("pages overlap or skip: %v", seen)
		}
	})
	t.Run("offset_beyond_total", func(t *testing.T) {
		items, total, err := svc.ListCredentials(ctx, ListCredentialsOptions{Provider: "qqmusic", Offset: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 || total != 4 {
			t.Errorf("offset overflow: %d %d", len(items), total)
		}
	})
}

func TestSetCredentialStatus_ActiveAndDisabled(t *testing.T) {
	svc, _, now := newTestService(t)
	ctx := context.Background()
	caps := []provider.Capability{provider.CapGetSong}
	if err := svc.AddCredential(ctx, Credential{
		ID: "x", Provider: "qqmusic", Capabilities: caps, Status: StatusActive, HealthScore: 0.5,
		CooldownUntil: now.Add(5 * time.Minute), FailCount: 7,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.SetCredentialStatus(ctx, "x", StatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	c, _ := svc.GetCredential(ctx, "x")
	if c.Status != StatusDisabled {
		t.Errorf("not disabled: %v", c.Status)
	}
	// disabled 不应清空 cooldown / failCount（保留诊断信息）
	if c.CooldownUntil.IsZero() || c.FailCount == 0 {
		t.Error("disable must preserve diagnostics")
	}

	if err := svc.SetCredentialStatus(ctx, "x", StatusActive); err != nil {
		t.Fatalf("activate: %v", err)
	}
	c, _ = svc.GetCredential(ctx, "x")
	if c.Status != StatusActive || !c.CooldownUntil.IsZero() || c.FailCount != 0 {
		t.Errorf("re-activate must clear cooldown & failCount: %+v", c)
	}
}

func TestSetCredentialStatus_RejectBanned(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_ = svc.AddCredential(ctx, Credential{ID: "x", Provider: "qqmusic", Status: StatusActive, HealthScore: 1})
	err := svc.SetCredentialStatus(ctx, "x", StatusBanned)
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("banned must be rejected, got %v", err)
	}
}

