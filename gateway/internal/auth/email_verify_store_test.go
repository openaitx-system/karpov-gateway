package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGenerateNumericCode_Length(t *testing.T) {
	for _, n := range []int{4, 6, 8} {
		c, err := GenerateNumericCode(n)
		if err != nil {
			t.Fatalf("gen(%d): %v", n, err)
		}
		if len(c) != n {
			t.Errorf("len(%q) = %d, want %d", c, len(c), n)
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				t.Errorf("non-digit: %q", c)
				break
			}
		}
	}
	// 边界值：超出范围回到默认 6 位
	c, _ := GenerateNumericCode(20)
	if len(c) != 6 {
		t.Errorf("oversize digits should fall back to 6: %q", c)
	}
}

func TestGenerateNumericCode_Distribution(t *testing.T) {
	// 100 次生成至少应有 50 种以上不同前缀（粗略验证 crypto/rand 行为）
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		c, _ := GenerateNumericCode(6)
		seen[c[:3]] = struct{}{}
	}
	if len(seen) < 50 {
		t.Errorf("low entropy: only %d unique 3-prefixes in 100 samples", len(seen))
	}
}

func TestMemEmailVerificationStore_PutGetDelete(t *testing.T) {
	s := NewMemEmailVerificationStore()
	ctx := context.Background()
	if _, err := s.GetCode(ctx, "missing@x.com"); !errors.Is(err, ErrEmailCodeMissing) {
		t.Errorf("missing should return ErrEmailCodeMissing, got %v", err)
	}
	if err := s.PutCode(ctx, "a@x.com", "111111", time.Minute); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.GetCode(ctx, "a@x.com")
	if err != nil || got != "111111" {
		t.Errorf("get = %q %v", got, err)
	}
	// 邮箱大小写无关
	got2, _ := s.GetCode(ctx, "A@X.com")
	if got2 != "111111" {
		t.Errorf("case-insensitive get failed: %q", got2)
	}
	_ = s.DeleteCode(ctx, "a@x.com")
	if _, err := s.GetCode(ctx, "a@x.com"); !errors.Is(err, ErrEmailCodeMissing) {
		t.Errorf("after delete should be missing, got %v", err)
	}
}

func TestMemEmailVerificationStore_RateLimit(t *testing.T) {
	s := NewMemEmailVerificationStore()
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		n, err := s.IncrSendCount(ctx, "email:a@x.com", time.Hour)
		if err != nil {
			t.Fatalf("incr: %v", err)
		}
		if n != i {
			t.Errorf("incr #%d returned %d, want %d", i, n, i)
		}
	}
	// 不同 key 计数互不影响
	n, _ := s.IncrSendCount(ctx, "ip:1.2.3.4", time.Hour)
	if n != 1 {
		t.Errorf("isolated key counter mixed up: %d", n)
	}
}

func TestMemEmailVerificationStore_LastSentCooldown(t *testing.T) {
	s := NewMemEmailVerificationStore()
	ctx := context.Background()
	now := time.Now()
	if t0, err := s.GetLastSentAt(ctx, "a@x.com"); err != nil || !t0.IsZero() {
		t.Errorf("initial last-sent should be zero: %v err=%v", t0, err)
	}
	if err := s.MarkSentAt(ctx, "a@x.com", now, time.Minute); err != nil {
		t.Fatalf("mark: %v", err)
	}
	got, _ := s.GetLastSentAt(ctx, "a@x.com")
	if got.Unix() != now.Unix() {
		t.Errorf("mark/get mismatch: got=%d want=%d", got.Unix(), now.Unix())
	}
	// cooldown <= 0 ⇒ MarkSentAt 不写入（用于关闭冷却的部署）
	s2 := NewMemEmailVerificationStore()
	_ = s2.MarkSentAt(ctx, "a@x.com", now, 0)
	gotZero, _ := s2.GetLastSentAt(ctx, "a@x.com")
	if !gotZero.IsZero() {
		t.Errorf("cooldown<=0 should skip MarkSentAt, got %v", gotZero)
	}
}
