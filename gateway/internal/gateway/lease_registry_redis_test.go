package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisRegistry(t *testing.T, ttl time.Duration) (*RedisLeaseRegistry, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	cli := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cli.Close() })
	reg := NewRedisLeaseRegistry(cli, RedisLeaseRegistryOptions{TTL: ttl})
	return reg, mr
}

func TestRedisLeaseRegistry_PutTakeAndApply(t *testing.T) {
	reg, mr := newRedisRegistry(t, 30*time.Second)

	expires, err := reg.Put(context.Background(), "lease-1", "cred-1")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if expires.IsZero() {
		t.Error("expires zero")
	}

	// Redis 应该实际写了 key
	if !mr.Exists("pool:lease:lease-1") {
		t.Fatal("key not in redis")
	}

	got := ""
	taken, err := reg.TakeAndApply(context.Background(), "lease-1", func(credID string) {
		got = credID
	})
	if err != nil || !taken || got != "cred-1" {
		t.Errorf("take: taken=%v got=%q err=%v", taken, got, err)
	}

	// GETDEL 后 key 不应再存在。
	if mr.Exists("pool:lease:lease-1") {
		t.Error("key not deleted after take")
	}

	// 重复 TakeAndApply 静默成功。
	called := false
	taken, err = reg.TakeAndApply(context.Background(), "lease-1", func(string) {
		called = true
	})
	if err != nil || taken || called {
		t.Errorf("dup take: taken=%v called=%v err=%v", taken, called, err)
	}
}

func TestRedisLeaseRegistry_Put_DuplicateLeaseID(t *testing.T) {
	reg, _ := newRedisRegistry(t, 30*time.Second)

	if _, err := reg.Put(context.Background(), "lease-x", "cred-a"); err != nil {
		t.Fatalf("first put: %v", err)
	}
	// 同 leaseID 第二次 Put 应被 SETNX 阻止。
	if _, err := reg.Put(context.Background(), "lease-x", "cred-b"); err == nil {
		t.Error("expected error on duplicate lease id")
	}
}

func TestRedisLeaseRegistry_TTL_Expiry(t *testing.T) {
	reg, mr := newRedisRegistry(t, 100*time.Millisecond)

	if _, err := reg.Put(context.Background(), "lease-1", "cred-1"); err != nil {
		t.Fatalf("put: %v", err)
	}

	// miniredis 不会自动按真实时间过期；用 FastForward 推进时钟。
	mr.FastForward(200 * time.Millisecond)

	called := false
	taken, err := reg.TakeAndApply(context.Background(), "lease-1", func(string) {
		called = true
	})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if taken || called {
		t.Errorf("expired lease: taken=%v called=%v", taken, called)
	}
}

func TestRedisLeaseRegistry_TakeAndApply_NotFound(t *testing.T) {
	reg, _ := newRedisRegistry(t, time.Minute)

	taken, err := reg.TakeAndApply(context.Background(), "ghost", func(string) {
		t.Fatal("apply must not be called")
	})
	if err != nil || taken {
		t.Errorf("ghost lease: taken=%v err=%v", taken, err)
	}
}

func TestRedisLeaseRegistry_NilClient_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil client")
		}
	}()
	_ = NewRedisLeaseRegistry(nil, RedisLeaseRegistryOptions{})
}

func TestPoolGRPCService_WithRedisRegistry_AcquireRelease(t *testing.T) {
	// 端到端：注入 RedisLeaseRegistry，跑 Acquire→Release→health 上升。
	reg, _ := newRedisRegistry(t, 30*time.Second)

	adapter, svc := newPoolAdapter(t)
	defer adapter.Close()
	// 替换默认 Mem registry 为 Redis registry。
	adapter.reg = reg

	if err := svc.AddCredential(context.Background(), seedCred("c1", 0.8)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := acquireGetSong(t, adapter, "qqmusic")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	releaseOK(t, adapter, resp.GetLeaseId())

	sum, _ := svc.HealthSummary(context.Background(), "qqmusic")
	// 0.8 + 0.01 = 0.81。
	if sum.AverageScore < 0.805 || sum.AverageScore > 0.815 {
		t.Errorf("avg health: %v want ~0.81", sum.AverageScore)
	}
}

func TestRedisLeaseRegistry_DefaultsApplied(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	cli := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = cli.Close() }()

	reg := NewRedisLeaseRegistry(cli, RedisLeaseRegistryOptions{})
	if reg.prefix != "pool:lease:" {
		t.Errorf("prefix default: %q", reg.prefix)
	}
	if reg.ttl != defaultLeaseTTL {
		t.Errorf("ttl default: %v", reg.ttl)
	}
}
