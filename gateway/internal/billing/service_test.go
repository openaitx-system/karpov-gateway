package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func newTestSvc(t *testing.T) (*Service, *MemRepo, *time.Time) {
	t.Helper()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	repo := NewMemRepo()
	svc := NewService(repo, Options{Clock: clock})
	return svc, repo, &now
}

func newOrder(t *testing.T, idem string, ttl time.Duration) *Order {
	t.Helper()
	o, err := NewOrder("u1", "pro", decimal.NewFromInt(99), "CNY", idem, ttl)
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	return o
}

func TestCreateOrder_Idempotency(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	o := newOrder(t, "key1", 30*time.Minute)
	o2, err := svc.CreateOrder(ctx, o)
	if err != nil || o2.ID != o.ID {
		t.Fatalf("first create: %v", err)
	}
	// 同 idempotency_key 第二次创建应返回原订单
	o3, err := svc.CreateOrder(ctx, newOrder(t, "key1", 30*time.Minute))
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if o3.ID != o.ID {
		t.Errorf("idempotency failed: got %s want %s", o3.ID, o.ID)
	}
}

func TestTransition_HappyPath(t *testing.T) {
	svc, _, now := newTestSvc(t)
	ctx := context.Background()
	o := newOrder(t, "key", 30*time.Minute)
	_, _ = svc.CreateOrder(ctx, o)

	got, err := svc.Transition(ctx, o.ID, EvtPayStart)
	if err != nil {
		t.Fatalf("pay_start: %v", err)
	}
	if got.Status != OrderPaying {
		t.Errorf("status: %v", got.Status)
	}
	got, err = svc.Transition(ctx, o.ID, EvtCallbackOK)
	if err != nil {
		t.Fatalf("callback_ok: %v", err)
	}
	if got.Status != OrderPaid {
		t.Errorf("status: %v", got.Status)
	}
	if !got.PaidAt.Equal(*now) {
		t.Errorf("paid_at not stamped: %v", got.PaidAt)
	}
	got, _ = svc.Transition(ctx, o.ID, EvtFulfill)
	if got.Status != OrderCompleted {
		t.Errorf("status: %v", got.Status)
	}
}

func TestTransition_Invalid(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	o := newOrder(t, "key", 30*time.Minute)
	_, _ = svc.CreateOrder(ctx, o)

	// PENDING → callback_ok 是非法转移
	_, err := svc.Transition(ctx, o.ID, EvtCallbackOK)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition: %v", err)
	}
}

func TestTransition_UserCancel(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	o := newOrder(t, "key", 30*time.Minute)
	_, _ = svc.CreateOrder(ctx, o)
	got, err := svc.Transition(ctx, o.ID, EvtUserCancel)
	if err != nil {
		t.Fatalf("user_cancel: %v", err)
	}
	if got.Status != OrderCanceled {
		t.Errorf("status: %v", got.Status)
	}
}

func TestSweepExpired(t *testing.T) {
	svc, repo, now := newTestSvc(t)
	ctx := context.Background()

	// 1) 已过期的 Pending（应被扫到）
	expired := newOrder(t, "k1", 1*time.Minute)
	expired.ExpiresAt = now.Add(-5 * time.Minute)
	_ = repo.Create(ctx, expired)

	// 2) 未过期 Pending（不应扫）
	fresh := newOrder(t, "k2", 30*time.Minute)
	fresh.ExpiresAt = now.Add(20 * time.Minute)
	_ = repo.Create(ctx, fresh)

	// 3) 已 PAID（不应扫）
	paid := newOrder(t, "k3", 30*time.Minute)
	paid.ExpiresAt = now.Add(-5 * time.Minute)
	paid.Status = OrderPaid
	_ = repo.Create(ctx, paid)

	n, err := svc.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("swept count: %d", n)
	}
	got, _ := repo.Get(ctx, expired.ID)
	if got.Status != OrderExpired {
		t.Errorf("expired status: %v", got.Status)
	}
}

func TestCanTransition_Matrix(t *testing.T) {
	cases := []struct {
		from  OrderStatus
		event string
		want  bool
	}{
		{OrderPending, EvtPayStart, true},
		{OrderPending, EvtCallbackOK, false}, // 跳级
		{OrderPaying, EvtCallbackOK, true},
		{OrderPaying, EvtCallbackFail, true},
		{OrderPaid, EvtFulfill, true},
		{OrderCompleted, EvtFulfill, false},
		{OrderCanceled, EvtPayStart, false},
		{OrderExpired, EvtPayStart, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.event); got != c.want {
			t.Errorf("(%s,%s) got %v want %v", c.from, c.event, got, c.want)
		}
	}
}

func TestNewOrder_DefaultsAndV7(t *testing.T) {
	o, err := NewOrder("u", "p", decimal.NewFromInt(1), "", "", 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if o.Currency != "CNY" {
		t.Errorf("default currency: %q", o.Currency)
	}
	if o.ID == "" {
		t.Errorf("uuid not set")
	}
	// uuid v7 第 13 位（version nibble）= 7
	if len(o.ID) >= 15 && o.ID[14] != '7' {
		t.Errorf("not uuidv7: %s", o.ID)
	}
}
