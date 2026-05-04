package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newBootstrapSvc(t *testing.T) (*Service, *MemUserRepo) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	repo := NewMemUserRepo()
	svc := NewService(repo, NewRedisSessionStore(rdb, ""), Options{
		PasswordParams:      testPwParams(),
		SessionTTL:          time.Hour,
		MinPasswordStrength: -1,
	})
	return svc, repo
}

func TestBootstrap_CreatesWhenAbsent(t *testing.T) {
	svc, _ := newBootstrapSvc(t)
	res, err := svc.Bootstrap(context.Background(), BootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !res.Created {
		t.Fatal("expected Created=true on empty store")
	}
	if res.Email != "admin@example.com" {
		t.Errorf("default email: %q", res.Email)
	}
	if res.UserID == "" {
		t.Error("missing user_id")
	}
	// 192bit base32 ~ 39 字符
	if len(res.PlainPassword) < 32 {
		t.Errorf("plain too short: %d", len(res.PlainPassword))
	}
	// base32 字符集仅 [A-Z2-7]
	for _, c := range res.PlainPassword {
		if !((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) {
			t.Errorf("non base32 char in password: %q", c)
			break
		}
	}
	u, err := svc.users.GetByEmail(context.Background(), "admin@example.com")
	if err != nil || u == nil {
		t.Fatalf("user not persisted: %v", err)
	}
	if u.Role != RoleSuperAdmin {
		t.Errorf("role: %s", u.Role)
	}
	// 用明文应该能 Login（验证 hash 写入正确）
	if _, err := svc.Login(context.Background(), res.Email, res.PlainPassword, "", "", ""); err != nil {
		t.Errorf("login with bootstrap password: %v", err)
	}
}

func TestBootstrap_IdempotentWhenEmailExists(t *testing.T) {
	svc, _ := newBootstrapSvc(t)
	first, err := svc.Bootstrap(context.Background(), BootstrapOptions{})
	if err != nil || !first.Created {
		t.Fatalf("first bootstrap: created=%v err=%v", first.Created, err)
	}
	second, err := svc.Bootstrap(context.Background(), BootstrapOptions{})
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if second.Created {
		t.Fatal("second call should not Create")
	}
	if second.UserID != first.UserID {
		t.Errorf("user id mismatch: %s vs %s", second.UserID, first.UserID)
	}
	if second.PlainPassword != "" {
		t.Errorf("non-create must not return plaintext, got %q", second.PlainPassword)
	}
}

func TestBootstrap_RespectsCustomEmail(t *testing.T) {
	svc, _ := newBootstrapSvc(t)
	res, err := svc.Bootstrap(context.Background(), BootstrapOptions{
		Email: "ops@example.com",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if res.Email != "ops@example.com" {
		t.Errorf("email: %q", res.Email)
	}
}

func TestBootstrap_DisabledShortCircuits(t *testing.T) {
	svc, _ := newBootstrapSvc(t)
	res, err := svc.Bootstrap(context.Background(), BootstrapOptions{Disabled: true})
	if !errors.Is(err, ErrBootstrapDisabled) {
		t.Errorf("expected ErrBootstrapDisabled, got %v", err)
	}
	if res == nil || res.Created {
		t.Errorf("disabled must not create: %+v", res)
	}
	// 用户也不应该被创建
	if u, _ := svc.users.GetByEmail(context.Background(), "admin@example.com"); u != nil {
		t.Errorf("disabled bootstrap still created user: %+v", u)
	}
}

func TestBootstrap_SkipsWhenNonSuperAdminWithSameEmail(t *testing.T) {
	svc, repo := newBootstrapSvc(t)
	// 预先植入同 email 的普通用户，模拟管理员手动建账后再启动
	hash, _ := HashPassword("Existing!Pass1", testPwParams())
	pre := &User{
		Email:        "admin@example.com",
		PasswordHash: hash,
		Status:       "active",
		Role:         RoleUser,
		CreatedAt:    time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), pre); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := svc.Bootstrap(context.Background(), BootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if res.Created {
		t.Fatal("must not create when email exists, even if role != superadmin")
	}
	if res.UserID != pre.ID {
		t.Errorf("expected return existing user_id, got %s vs %s", res.UserID, pre.ID)
	}
	got, _ := repo.GetByEmail(context.Background(), "admin@example.com")
	if got == nil || got.Role != RoleUser {
		t.Errorf("role got overwritten: %+v", got)
	}
}

func TestPrintBootstrapBanner(t *testing.T) {
	var buf bytes.Buffer
	r := &BootstrapResult{
		Created:       true,
		Email:         "admin@example.com",
		UserID:        "u_x",
		PlainPassword: "ABCDE2FGHIJK3LMNO4PQRSTU5VWXYZ6789AAAAA",
	}
	if !PrintBootstrapBanner(&buf, r) {
		t.Fatal("expected true on Created result")
	}
	out := buf.String()
	for _, want := range []string{
		"superadmin",
		"admin@example.com",
		"u_x",
		"ABCDE2FGHIJK3LMNO4PQRSTU5VWXYZ6789AAAAA",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q: %q", want, out)
		}
	}

	// non-created → no output, returns false
	buf.Reset()
	if PrintBootstrapBanner(&buf, &BootstrapResult{Created: false}) {
		t.Error("expected false when not Created")
	}
	if buf.Len() != 0 {
		t.Errorf("banner wrote when not Created: %q", buf.String())
	}
	if PrintBootstrapBanner(&buf, nil) {
		t.Error("nil result should yield false")
	}
}

func TestGenerateBootstrapPassword_FloorClampsTo24(t *testing.T) {
	p, err := generateBootstrapPassword(0)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	// 24B base32 nopadding ≈ ceil(24*8/5) = 39
	if len(p) < 32 {
		t.Errorf("len: %d", len(p))
	}
}
