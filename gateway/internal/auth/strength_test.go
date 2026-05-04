package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// stubPwnedChecker 实现 PwnedChecker 接口，模拟不同响应。
type stubPwnedChecker struct {
	pwned bool
	err   error
}

func (s stubPwnedChecker) IsPwned(_ context.Context, _ string) (bool, error) {
	return s.pwned, s.err
}

func newAuthSvcWithPwned(t *testing.T, p PwnedChecker) *Service {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	repo := NewMemUserRepo()
	sess := NewRedisSessionStore(rdb, "")
	return NewService(repo, sess, Options{
		PasswordParams:      testPwParams(),
		SessionTTL:          1 * time.Hour,
		MinPasswordStrength: StrengthSafelyUnguess,
		PwnedChecker:        p,
	})
}

func TestEvaluatePassword_Buckets(t *testing.T) {
	cases := []struct {
		name     string
		password string
		min      PasswordStrength
		max      PasswordStrength
	}{
		{"empty", "", StrengthTooGuessable, StrengthTooGuessable},
		{"weak_dict_password", "password", StrengthTooGuessable, StrengthTooGuessable},
		{"weak_dict_qwerty", "qwerty", StrengthTooGuessable, StrengthTooGuessable},
		{"weak_dict_hunter22", "hunter22", StrengthTooGuessable, StrengthTooGuessable},
		{"weak_dict_caseinsens", "PASSWORD", StrengthTooGuessable, StrengthTooGuessable},
		{"too_short", "ab12", StrengthTooGuessable, StrengthTooGuessable},
		{"len_6_one_class", "abcxyz", StrengthVeryGuessable, StrengthVeryGuessable},
		{"len_8_two_classes", "abcd1234", StrengthVeryGuessable, StrengthVeryGuessable},
		{"len_12_two_classes", "abcdefgh1234", StrengthSomewhat, StrengthSomewhat},
		{"len_10_three_classes", "Abcd1234ef", StrengthSomewhat, StrengthSomewhat},
		{"len_12_three_classes", "Abcd1234efgh", StrengthSafelyUnguess, StrengthSafelyUnguess},
		{"len_10_four_classes", "Ab1!cdEf2@", StrengthSafelyUnguess, StrengthSafelyUnguess},
		{"len_12_four_classes", "Ab1!cdEf2@gH", StrengthVeryUnguessable, StrengthVeryUnguessable},
		{"sequence_12345678", "12345678", StrengthTooGuessable, StrengthVeryGuessable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EvaluatePassword(c.password)
			if got < c.min || got > c.max {
				t.Errorf("score=%d not in [%d,%d]", got, c.min, c.max)
			}
		})
	}
}

func TestRegister_RejectsWeakPassword(t *testing.T) {
	// 使用默认 strength 策略（不传 MinPasswordStrength）
	svc, _, _, _ := newAuthSvc(t)
	// 把 minStrength 改回默认值
	svc.minStrength = StrengthSafelyUnguess

	ctx := t.Context()
	_, err := svc.Register(ctx, "weak@x.y", "password", "", "")
	if err != ErrWeakPassword {
		t.Errorf("expected ErrWeakPassword, got %v", err)
	}

	// 强密码通过
	if _, err := svc.Register(ctx, "strong@x.y", "Ab1!cdEf2@gH", "", ""); err != nil {
		t.Errorf("strong password rejected: %v", err)
	}
}

func TestRegister_RejectsPwnedPassword(t *testing.T) {
	svc := newAuthSvcWithPwned(t, stubPwnedChecker{pwned: true})

	// 强度通过但 HIBP 命中 → ErrPwnedPassword
	_, err := svc.Register(t.Context(), "a@b.c", "Ab1!cdEf2@gH", "", "")
	if !errors.Is(err, ErrPwnedPassword) {
		t.Errorf("expected ErrPwnedPassword, got %v", err)
	}
}

func TestRegister_FailOpenOnHIBPError(t *testing.T) {
	// HIBP 网络故障 → fail-open，注册应当通过
	svc := newAuthSvcWithPwned(t, stubPwnedChecker{err: errors.New("hibp: 429")})

	u, err := svc.Register(t.Context(), "a@b.c", "Ab1!cdEf2@gH", "", "")
	if err != nil {
		t.Errorf("HIBP failure should fail-open, got %v", err)
	}
	if u == nil || u.ID == "" {
		t.Errorf("user not created")
	}
}

func TestRegister_PwnedCheckerSkippedOnWeakPassword(t *testing.T) {
	// 弱密码应在 strength 阶段被拒，HIBP 不应被调用
	called := false
	stub := stubPwnedCheckerCounting{called: &called}
	svc := newAuthSvcWithPwned(t, stub)
	svc.minStrength = StrengthSafelyUnguess

	_, err := svc.Register(t.Context(), "a@b.c", "password", "", "")
	if !errors.Is(err, ErrWeakPassword) {
		t.Errorf("expected ErrWeakPassword, got %v", err)
	}
	if called {
		t.Errorf("HIBP must not be called when strength check fails")
	}
}

type stubPwnedCheckerCounting struct {
	called *bool
}

func (s stubPwnedCheckerCounting) IsPwned(_ context.Context, _ string) (bool, error) {
	*s.called = true
	return false, nil
}

func TestIsMonotonicSequence(t *testing.T) {
	if !isMonotonicSequence("12345") {
		t.Errorf("12345 should be monotonic")
	}
	if !isMonotonicSequence("abcdef") {
		t.Errorf("abcdef should be monotonic")
	}
	if isMonotonicSequence("13579") {
		t.Errorf("13579 not consecutive, should not be monotonic")
	}
	if isMonotonicSequence("abc") {
		t.Errorf("too short, should not be monotonic")
	}
}
