package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestMemBalanceRepo_SetBalance_FromZero(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	bal, tx, err := r.SetBalance(ctx, SetRequest{
		UserID: "u1", NewBalanceCents: 5000, Description: "initial",
	})
	if err != nil {
		t.Fatalf("SetBalance: %v", err)
	}
	if bal.BalanceCents != 5000 {
		t.Fatalf("expected 5000, got %d", bal.BalanceCents)
	}
	if tx.AmountCents != 5000 {
		t.Fatalf("expected delta 5000, got %d", tx.AmountCents)
	}
	if tx.Kind != BalanceKindAdjust {
		t.Fatalf("expected adjust kind, got %s", tx.Kind)
	}
}

func TestMemBalanceRepo_SetBalance_Decreases(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	_, _, _ = r.Credit(ctx, CreditRequest{UserID: "u1", AmountCents: 1000, Kind: BalanceKindTopup})
	bal, tx, err := r.SetBalance(ctx, SetRequest{UserID: "u1", NewBalanceCents: 200})
	if err != nil {
		t.Fatalf("SetBalance: %v", err)
	}
	if bal.BalanceCents != 200 {
		t.Fatalf("expected 200, got %d", bal.BalanceCents)
	}
	if tx.AmountCents != -800 {
		t.Fatalf("expected delta -800, got %d", tx.AmountCents)
	}
}

func TestMemBalanceRepo_SetBalance_Negative_Rejects(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	_, _, err := r.SetBalance(ctx, SetRequest{UserID: "u1", NewBalanceCents: -1})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestMemBalanceRepo_SetBalance_LedgerCorrect(t *testing.T) {
	r := NewMemBalanceRepo()
	ctx := context.Background()
	// 起始 100
	_, _, _ = r.Credit(ctx, CreditRequest{UserID: "u1", AmountCents: 100, Kind: BalanceKindTopup})
	// 设到 250 → 流水 +150
	_, _, _ = r.SetBalance(ctx, SetRequest{UserID: "u1", NewBalanceCents: 250})
	// 设到 50 → 流水 -200
	_, _, _ = r.SetBalance(ctx, SetRequest{UserID: "u1", NewBalanceCents: 50})

	items, total, err := r.ListTransactions(ctx, "u1", 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 transactions, got %d", total)
	}
	// 倒序：[-200, +150, +100]
	if items[0].AmountCents != -200 {
		t.Fatalf("expected newest -200, got %d", items[0].AmountCents)
	}
	if items[1].AmountCents != 150 {
		t.Fatalf("expected mid +150, got %d", items[1].AmountCents)
	}
	if items[0].BalanceAfterCents != 50 || items[1].BalanceAfterCents != 250 {
		t.Fatalf("unexpected balance_after sequence: %+v", items)
	}
}
