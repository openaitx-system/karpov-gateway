package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemExtraUsageRepo_GetSettings_DefaultsWhenAbsent(t *testing.T) {
	r := NewMemExtraUsageRepo()
	s, err := r.GetSettings(context.Background(), "u1")
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if s.UserID != "u1" || s.Enabled || s.MonthlyCapCents != 0 || s.NotifyThresholdPct != 80 {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}

func TestMemExtraUsageRepo_UpsertAndGet(t *testing.T) {
	r := NewMemExtraUsageRepo()
	in := &ExtraUsageSettings{UserID: "u1", Enabled: true, MonthlyCapCents: 5000, NotifyThresholdPct: 50}
	if err := r.UpsertSettings(context.Background(), in); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	out, err := r.GetSettings(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !out.Enabled || out.MonthlyCapCents != 5000 || out.NotifyThresholdPct != 50 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt should be set on upsert")
	}
}

func TestMemExtraUsageRepo_AddCharge_Accumulates(t *testing.T) {
	r := NewMemExtraUsageRepo()
	ctx := context.Background()
	const month = "2026-05"
	c1, err := r.AddCharge(ctx, "u1", month, "pro", 1, 10)
	if err != nil {
		t.Fatalf("AddCharge1: %v", err)
	}
	if c1.Count != 1 || c1.WeightSum != 1 || c1.AmountCents != 10 {
		t.Fatalf("unexpected first charge: %+v", c1)
	}
	c2, err := r.AddCharge(ctx, "u1", month, "pro", 3, 25)
	if err != nil {
		t.Fatalf("AddCharge2: %v", err)
	}
	if c2.Count != 2 || c2.WeightSum != 4 || c2.AmountCents != 35 {
		t.Fatalf("expected accumulation, got: %+v", c2)
	}
}

// 帮助函数：构造一个已开启 Extra Usage 且充值充足的 Service。
func newServiceWithBalance(t *testing.T, userID string, enabled bool, cap int64, balanceCents int64) *ExtraUsageService {
	t.Helper()
	repo := NewMemExtraUsageRepo()
	if enabled {
		_ = repo.UpsertSettings(context.Background(), &ExtraUsageSettings{
			UserID: userID, Enabled: true, MonthlyCapCents: cap, NotifyThresholdPct: 80,
		})
	}
	bal := NewMemBalanceRepo()
	if balanceCents > 0 {
		if _, _, err := bal.Credit(context.Background(), CreditRequest{
			UserID: userID, AmountCents: balanceCents, Kind: BalanceKindTopup,
			Description: "test seed",
		}); err != nil {
			t.Fatalf("seed credit: %v", err)
		}
	}
	return NewExtraUsageService(repo, bal)
}

func TestExtraUsageService_Disabled_Rejects(t *testing.T) {
	svc := newServiceWithBalance(t, "u1", false, 0, 1000)
	dec, err := svc.CheckAndCharge(context.Background(), "u1", "pro", "qqmusic", "songs", 1, 100)
	if err != nil {
		t.Fatalf("CheckAndCharge: %v", err)
	}
	if dec.Allow {
		t.Fatalf("expected reject when disabled")
	}
	if dec.Reason != "extra_usage_disabled" {
		t.Fatalf("unexpected reason: %s", dec.Reason)
	}
}

func TestExtraUsageService_PlanNotPayAsYouGo(t *testing.T) {
	svc := newServiceWithBalance(t, "u1", true, 0, 1000)
	dec, err := svc.CheckAndCharge(context.Background(), "u1", "free", "qqmusic", "songs", 1, 0)
	if err != nil {
		t.Fatalf("CheckAndCharge: %v", err)
	}
	if dec.Allow || dec.Reason != "plan_not_pay_as_you_go" {
		t.Fatalf("expected plan_not_pay_as_you_go, got: %+v", dec)
	}
}

func TestExtraUsageService_Allows_AndDeductsBalance(t *testing.T) {
	svc := newServiceWithBalance(t, "u1", true, 0, 1000)

	// pricePer1k=100 cents/千次, weight=1 → ceil(100/1000) = 1 cent
	dec, err := svc.CheckAndCharge(context.Background(), "u1", "pro", "qqmusic", "songs/1234", 1, 100)
	if err != nil {
		t.Fatalf("CheckAndCharge: %v", err)
	}
	if !dec.Allow || !dec.Charged {
		t.Fatalf("expected Allow+Charged, got: %+v", dec)
	}
	if dec.PriceCents != 1 {
		t.Fatalf("expected 1 cent, got %d", dec.PriceCents)
	}
	if dec.UsedCents != 1 {
		t.Fatalf("expected used 1 cent, got %d", dec.UsedCents)
	}
	if dec.BalanceAfterCents != 999 {
		t.Fatalf("expected balance 999, got %d", dec.BalanceAfterCents)
	}

	// 第二次：累加
	dec2, _ := svc.CheckAndCharge(context.Background(), "u1", "pro", "qqmusic", "songs/1234", 5, 100)
	if dec2.UsedCents != 2 {
		t.Fatalf("expected used 2 cents, got %d", dec2.UsedCents)
	}
	if dec2.BalanceAfterCents != 998 {
		t.Fatalf("expected balance 998, got %d", dec2.BalanceAfterCents)
	}
}

func TestExtraUsageService_InsufficientBalance_Rejects(t *testing.T) {
	svc := newServiceWithBalance(t, "u1", true, 0, 0) // 余额为 0
	dec, _ := svc.CheckAndCharge(context.Background(), "u1", "pro", "qq", "ep", 1, 100)
	if dec.Allow {
		t.Fatalf("expected reject on zero balance")
	}
	if dec.Reason != "insufficient_balance" {
		t.Fatalf("unexpected reason: %s", dec.Reason)
	}
}

func TestExtraUsageService_CapExceeded_Rejects_BalanceUntouched(t *testing.T) {
	svc := newServiceWithBalance(t, "u1", true, 5, 1000) // cap=5 cents

	// 5 次 1 cent 花完 cap
	for i := 0; i < 5; i++ {
		dec, _ := svc.CheckAndCharge(context.Background(), "u1", "pro", "qq", "ep", 1, 100)
		if !dec.Allow {
			t.Fatalf("iteration %d unexpectedly rejected: %+v", i, dec)
		}
	}
	// 第 6 次：cap 超出
	dec, _ := svc.CheckAndCharge(context.Background(), "u1", "pro", "qq", "ep", 1, 100)
	if dec.Allow {
		t.Fatalf("expected reject after cap exhausted, got: %+v", dec)
	}
	if dec.Reason != "extra_usage_cap_exceeded" {
		t.Fatalf("unexpected reason: %s", dec.Reason)
	}
	// 余额应剩 1000-5=995（cap 拒绝在扣费之前）
	bal, _ := svc.balance.Get(context.Background(), "u1")
	if bal.BalanceCents != 995 {
		t.Fatalf("expected balance 995 (5 spent), got %d", bal.BalanceCents)
	}
}

func TestExtraUsageService_RecordsEvents(t *testing.T) {
	svc := newServiceWithBalance(t, "u1", true, 0, 1000)
	for i := 0; i < 3; i++ {
		_, _ = svc.CheckAndCharge(context.Background(), "u1", "pro", "qq", "ep", 1, 100)
	}
	repo := svc.repo
	evs, err := repo.ListEvents(context.Background(), "u1", 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("expected 3 events, got %d", len(evs))
	}
	if evs[0].Timestamp.Before(evs[len(evs)-1].Timestamp) {
		t.Fatalf("expected newest-first ordering")
	}
}

func TestSplitProviderEndpoint(t *testing.T) {
	cases := []struct {
		path, prov, ep string
	}{
		{"/v1/qqmusic/songs/123", "qqmusic", "songs/123"},
		{"/v1/netease", "netease", ""},
		{"/", "", ""},
	}
	for _, c := range cases {
		p, e := splitProviderEndpoint(c.path)
		if p != c.prov || e != c.ep {
			t.Errorf("%s: got (%s,%s), want (%s,%s)", c.path, p, e, c.prov, c.ep)
		}
	}
}

func TestExtraUsageService_PriceRounding(t *testing.T) {
	svc := newServiceWithBalance(t, "u1", true, 0, 100000)

	// pricePer1k=50, weight=1 → 50/1000=0.05 → ceil=1
	dec, _ := svc.CheckAndCharge(context.Background(), "u1", "ent", "qq", "ep", 1, 50)
	if dec.PriceCents != 1 {
		t.Fatalf("ceil(50/1000) should be 1 cent, got %d", dec.PriceCents)
	}

	// pricePer1k=50, weight=20 → 1000/1000=1 → ceil=1
	dec2, _ := svc.CheckAndCharge(context.Background(), "u1", "ent", "qq", "ep", 20, 50)
	if dec2.PriceCents != 1 {
		t.Fatalf("ceil(1000/1000) should be 1 cent, got %d", dec2.PriceCents)
	}

	// pricePer1k=100, weight=21 → 2100/1000=2.1 → ceil=3
	dec3, _ := svc.CheckAndCharge(context.Background(), "u1", "ent", "qq", "ep", 21, 100)
	if dec3.PriceCents != 3 {
		t.Fatalf("ceil(2100/1000) should be 3 cents, got %d", dec3.PriceCents)
	}
}

func TestMemExtraUsageRepo_ListCharges_OrderedDesc(t *testing.T) {
	r := NewMemExtraUsageRepo()
	ctx := context.Background()
	for _, m := range []string{"2026-03", "2026-05", "2026-04"} {
		if _, err := r.AddCharge(ctx, "u1", m, "pro", 1, 100); err != nil {
			t.Fatalf("AddCharge %s: %v", m, err)
		}
	}
	out, err := r.ListCharges(ctx, "u1", 10)
	if err != nil {
		t.Fatalf("ListCharges: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 charges, got %d", len(out))
	}
	if out[0].YearMonth != "2026-05" || out[1].YearMonth != "2026-04" || out[2].YearMonth != "2026-03" {
		t.Fatalf("expected DESC by yearMonth, got: %v", []string{out[0].YearMonth, out[1].YearMonth, out[2].YearMonth})
	}
}

func TestMemExtraUsageRepo_RecordEvent_DefaultTimestamp(t *testing.T) {
	r := NewMemExtraUsageRepo()
	ctx := context.Background()
	before := time.Now().UTC().Add(-1 * time.Second)
	_ = r.RecordEvent(ctx, &OverageEvent{UserID: "u1", Provider: "qq", Endpoint: "ep", Weight: 1, PriceCents: 1})
	evs, _ := r.ListEvents(ctx, "u1", 1)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event")
	}
	if evs[0].Timestamp.Before(before) {
		t.Fatalf("timestamp should be recent: %v", evs[0].Timestamp)
	}
	if evs[0].ID == 0 {
		t.Fatalf("ID should be assigned")
	}
}

// ---- 余额仓库测试 ----

func TestMemBalanceRepo_GetDefaultsZero(t *testing.T) {
	r := NewMemBalanceRepo()
	b, err := r.Get(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if b.BalanceCents != 0 || b.Currency != "CNY" {
		t.Fatalf("unexpected default: %+v", b)
	}
}

func TestMemBalanceRepo_CreditDebit_LedgerConsistent(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	bal, tx, err := r.Credit(ctx, CreditRequest{
		UserID: "u1", AmountCents: 1000, Kind: BalanceKindTopup, OrderID: "ord_1",
	})
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if bal.BalanceCents != 1000 || tx.AmountCents != 1000 || tx.BalanceAfterCents != 1000 {
		t.Fatalf("unexpected after credit: bal=%d tx=%+v", bal.BalanceCents, tx)
	}

	bal2, tx2, err := r.Debit(ctx, DebitRequest{
		UserID: "u1", AmountCents: 250, Kind: BalanceKindOverage,
	})
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if bal2.BalanceCents != 750 || tx2.AmountCents != -250 || tx2.BalanceAfterCents != 750 {
		t.Fatalf("unexpected after debit: bal=%d tx=%+v", bal2.BalanceCents, tx2)
	}
}

func TestMemBalanceRepo_Debit_InsufficientBalance(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	_, _, err := r.Debit(ctx, DebitRequest{UserID: "u1", AmountCents: 1, Kind: BalanceKindOverage})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestMemBalanceRepo_InvalidAmount(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	if _, _, err := r.Credit(ctx, CreditRequest{UserID: "u1", AmountCents: 0, Kind: BalanceKindTopup}); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("expected invalid amount, got %v", err)
	}
	if _, _, err := r.Debit(ctx, DebitRequest{UserID: "u1", AmountCents: -1, Kind: BalanceKindOverage}); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("expected invalid amount, got %v", err)
	}
}

func TestMemBalanceRepo_ListTransactions_NewestFirst(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, _, err := r.Credit(ctx, CreditRequest{
			UserID: "u1", AmountCents: 100, Kind: BalanceKindTopup,
			Description: "test",
		}); err != nil {
			t.Fatalf("Credit %d: %v", i, err)
		}
	}
	items, total, err := r.ListTransactions(ctx, "u1", 3, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	// 倒序：最新第一
	if items[0].ID < items[2].ID {
		t.Fatalf("expected newest first")
	}

	// 偏移量
	page2, _, _ := r.ListTransactions(ctx, "u1", 3, 3)
	if len(page2) != 2 {
		t.Fatalf("expected 2 items in second page, got %d", len(page2))
	}
}

func TestMemBalanceRepo_VersionIncrements(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	b1, _, _ := r.Credit(ctx, CreditRequest{UserID: "u1", AmountCents: 10, Kind: BalanceKindTopup})
	b2, _, _ := r.Credit(ctx, CreditRequest{UserID: "u1", AmountCents: 10, Kind: BalanceKindTopup})
	if b2.Version <= b1.Version {
		t.Fatalf("version should increment: %d → %d", b1.Version, b2.Version)
	}
}

// 综合：Extra Usage 与 Balance 协同
func TestExtraUsage_DeductsFromBalance_EndToEnd(t *testing.T) {
	repo := NewMemExtraUsageRepo()
	bal := NewMemBalanceRepo()
	ctx := context.Background()

	// 设置：开启，cap=0(无)
	_ = repo.UpsertSettings(ctx, &ExtraUsageSettings{
		UserID: "u1", Enabled: true, MonthlyCapCents: 0, NotifyThresholdPct: 80,
	})
	// 充值 100 cents
	_, _, _ = bal.Credit(ctx, CreditRequest{UserID: "u1", AmountCents: 100, Kind: BalanceKindTopup})

	svc := NewExtraUsageService(repo, bal)
	// 100 次扣 1 cent → 余额变 0
	for i := 0; i < 100; i++ {
		dec, err := svc.CheckAndCharge(ctx, "u1", "pro", "qq", "ep", 1, 100)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if !dec.Allow {
			t.Fatalf("iter %d unexpectedly rejected: %+v", i, dec)
		}
	}
	// 第 101 次余额不足
	dec, _ := svc.CheckAndCharge(ctx, "u1", "pro", "qq", "ep", 1, 100)
	if dec.Allow {
		t.Fatalf("expected insufficient balance after 100 iters")
	}
	if dec.Reason != "insufficient_balance" {
		t.Fatalf("unexpected reason: %s", dec.Reason)
	}

	// 余额已经清零
	cur, _ := bal.Get(ctx, "u1")
	if cur.BalanceCents != 0 {
		t.Fatalf("expected zero balance, got %d", cur.BalanceCents)
	}

	// overage_charges 累计 100 次 / 100 cent
	c, _ := repo.GetCharge(ctx, "u1", time.Now().UTC().Format("2006-01"))
	if c.Count != 100 || c.AmountCents != 100 {
		t.Fatalf("unexpected charge totals: %+v", c)
	}
}
