package quota

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestSvc(t *testing.T) (*Service, *miniredis.Miniredis, time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	svc := NewService(rdb, Options{
		Clock: func() time.Time { return now },
	})
	return svc, mr, now
}

func TestCheckAndConsume_BasicAllow(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()

	r := Rules{
		UserID: "u1", Provider: "qqmusic", Endpoint: "GetSong",
		Weight: 1, DayLimit: 100, MonthLimit: 1000, SoftLimitPct: 80,
	}
	res, err := svc.CheckAndConsume(ctx, r)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Errorf("decision: %v", res.Decision)
	}
	if res.DayUsed != 1 || res.MonthUsed != 1 {
		t.Errorf("counters: day=%d month=%d", res.DayUsed, res.MonthUsed)
	}
}

func TestCheckAndConsume_WeightApplied(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	r := Rules{UserID: "u1", Provider: "p", Endpoint: "GetSongURL", Weight: 3, DayLimit: 100, MonthLimit: 1000}
	res, _ := svc.CheckAndConsume(ctx, r)
	if res.DayUsed != 3 || res.MonthUsed != 3 {
		t.Errorf("weighted: day=%d month=%d", res.DayUsed, res.MonthUsed)
	}
}

func TestCheckAndConsume_HardLimit_Day(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	r := Rules{UserID: "u1", Provider: "p", Endpoint: "x", Weight: 1, DayLimit: 5, MonthLimit: 100}
	for i := 0; i < 5; i++ {
		res, err := svc.CheckAndConsume(ctx, r)
		if err != nil {
			t.Fatalf("err #%d: %v", i, err)
		}
		if res.Decision != DecisionAllow {
			t.Errorf("call #%d should allow: %v", i, res.Decision)
		}
	}
	// 第 6 次应当 hard limit
	res, _ := svc.CheckAndConsume(ctx, r)
	if res.Decision != DecisionHardLimit {
		t.Errorf("6th decision: %v", res.Decision)
	}
	// 撤回后 day_used 应该回到 5
	if res.DayUsed != 5 {
		t.Errorf("after revert day_used: %d", res.DayUsed)
	}
	// retry_after_ms 应该非零
	if res.RetryAfterMs <= 0 {
		t.Errorf("hard limit retry_after_ms missing")
	}
}

func TestCheckAndConsume_SoftLimit(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	// soft_pct=50, month_limit=10 → 月用量 > 5 触发 soft
	r := Rules{UserID: "u1", Provider: "p", Endpoint: "x", Weight: 1, DayLimit: 100, MonthLimit: 10, SoftLimitPct: 50}
	for i := 0; i < 5; i++ {
		res, _ := svc.CheckAndConsume(ctx, r)
		if res.Decision != DecisionAllow {
			t.Errorf("call #%d should allow: %v", i, res.Decision)
		}
	}
	// 第 6 次应当 soft（month_used=6, 6*100 > 10*50）
	res, _ := svc.CheckAndConsume(ctx, r)
	if res.Decision != DecisionSoftLimit {
		t.Errorf("6th decision: %v", res.Decision)
	}
	if res.RetryAfterMs <= 0 {
		t.Errorf("soft limit retry_after_ms missing")
	}
	if res.MonthUsed != 6 {
		t.Errorf("soft preserves counter: month=%d", res.MonthUsed)
	}
}

func TestRefund(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	r := Rules{UserID: "u1", Provider: "p", Endpoint: "x", Weight: 3, DayLimit: 100, MonthLimit: 1000}
	_, _ = svc.CheckAndConsume(ctx, r)
	_, _ = svc.CheckAndConsume(ctx, r)
	// usage = 6
	res, err := svc.Refund(ctx, r)
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if res.DayUsed != 3 || res.MonthUsed != 3 {
		t.Errorf("after refund: day=%d month=%d", res.DayUsed, res.MonthUsed)
	}
}

func TestRefund_NoNegative(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	r := Rules{UserID: "u1", Provider: "p", Endpoint: "x", Weight: 5, DayLimit: 100, MonthLimit: 1000}
	// 没有先 consume；refund 不应让计数器变成 -5
	res, _ := svc.Refund(ctx, r)
	if res.DayUsed != 0 || res.MonthUsed != 0 {
		t.Errorf("refund without consume: day=%d month=%d", res.DayUsed, res.MonthUsed)
	}
}

func TestGetUsage(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	r := Rules{UserID: "u1", Provider: "p", Endpoint: "x", Weight: 2, DayLimit: 100, MonthLimit: 1000}
	_, _ = svc.CheckAndConsume(ctx, r)
	_, _ = svc.CheckAndConsume(ctx, r)
	usage, err := svc.GetUsage(ctx, r)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if usage.DayUsed != 4 || usage.MonthUsed != 4 {
		t.Errorf("usage: day=%d month=%d", usage.DayUsed, usage.MonthUsed)
	}
}

func TestCheckAndConsume_NoLimit(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	r := Rules{UserID: "u1", Provider: "p", Endpoint: "x", Weight: 1, DayLimit: 0, MonthLimit: 0}
	for i := 0; i < 100; i++ {
		res, _ := svc.CheckAndConsume(ctx, r)
		if res.Decision != DecisionAllow {
			t.Fatalf("call #%d should always allow with no limits: %v", i, res.Decision)
		}
	}
}
