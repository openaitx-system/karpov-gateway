package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newSvcWithAPIKeys 复用 newAuthSvc 的风格但额外注入 APIKeyRepo。
func newSvcWithAPIKeys(t *testing.T) (*Service, *User) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	users := NewMemUserRepo()
	sessions := NewRedisSessionStore(rdb, "")
	svc := NewService(users, sessions, Options{
		PasswordParams:      testPwParams(),
		SessionTTL:          time.Hour,
		MinPasswordStrength: -1,
		APIKeyRepo:          NewMemAPIKeyRepo(),
	})
	if _, err := svc.Register(context.Background(), "u@x.com", "P@ssw0rd!Strong", "", ""); err != nil {
		t.Fatalf("register: %v", err)
	}
	user, _ := users.GetByEmail(context.Background(), "u@x.com")
	return svc, user
}

func TestService_CreateAPIKey_ListAndVerify(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)

	plain, rec, err := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{
		Name:   "ci-token",
		Scopes: []string{"music:read"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(plain) < 11 || plain[:3] != "mk_" {
		t.Errorf("plain: %q", plain)
	}
	if rec.Prefix != plain[:8] {
		t.Errorf("prefix mismatch: %s vs %s", rec.Prefix, plain[:8])
	}
	if !rec.Enabled {
		t.Error("new key should be enabled by default")
	}

	list, _ := svc.ListAPIKeys(context.Background(), user.ID)
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Errorf("list: %+v", list)
	}

	gotUser, gotRec, err := svc.VerifyAPIKey(context.Background(), plain, "")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if gotUser.ID != user.ID || gotRec.ID != rec.ID {
		t.Errorf("verify mismatch")
	}
	if gotRec.LastUsedAt.IsZero() {
		t.Error("LastUsedAt should be touched")
	}
}

func TestService_RevokeAPIKey(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	plain, rec, _ := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{Name: "k"})

	if err := svc.RevokeAPIKey(context.Background(), user.ID, rec.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := svc.VerifyAPIKey(context.Background(), plain, ""); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("expected ErrAPIKeyInvalid after revoke, got %v", err)
	}
}

func TestService_RevokeAPIKey_OtherUser_Forbidden(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	_, rec, _ := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{Name: "k"})

	if _, err := svc.Register(context.Background(), "other@x.com", "P@ssw0rd!Strong", "", ""); err != nil {
		t.Fatalf("register other: %v", err)
	}
	other, _ := svc.users.GetByEmail(context.Background(), "other@x.com")

	if err := svc.RevokeAPIKey(context.Background(), other.ID, rec.ID); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("cross-user revoke should be NotFound, got %v", err)
	}
}

func TestService_VerifyAPIKey_Expired(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	plain, _, _ := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{
		Name:      "k",
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	if _, _, err := svc.VerifyAPIKey(context.Background(), plain, ""); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("expected ErrAPIKeyInvalid for expired key, got %v", err)
	}
}

func TestService_SetAPIKeyEnabled(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	plain, rec, _ := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{Name: "toggle-test"})

	// 禁用后验证应失败
	if err := svc.SetAPIKeyEnabled(context.Background(), user.ID, rec.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, _, err := svc.VerifyAPIKey(context.Background(), plain, ""); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("disabled key should not verify, got %v", err)
	}

	// 重新启用后验证应成功
	if err := svc.SetAPIKeyEnabled(context.Background(), user.ID, rec.ID, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, _, err := svc.VerifyAPIKey(context.Background(), plain, ""); err != nil {
		t.Errorf("re-enabled key should verify, got %v", err)
	}
}

func TestService_VerifyAPIKey_Malformed(t *testing.T) {
	svc, _ := newSvcWithAPIKeys(t)
	if _, _, err := svc.VerifyAPIKey(context.Background(), "not-a-key", ""); !errors.Is(err, ErrAPIKeyMalformed) {
		t.Errorf("expected ErrAPIKeyMalformed, got %v", err)
	}
}

func TestUser_Roles(t *testing.T) {
	u := &User{}
	if u.EffectiveRole() != RoleUser {
		t.Errorf("default role: %s", u.EffectiveRole())
	}
	if u.IsAdmin() {
		t.Error("default user should not be admin")
	}
	u.Role = RoleAdmin
	if !u.IsAdmin() {
		t.Error("admin should be admin")
	}
	u.Role = RoleSuperAdmin
	if !u.IsAdmin() {
		t.Error("superadmin should be admin")
	}
}

func TestService_SetRole(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)

	if err := svc.SetRole(context.Background(), user.ID, RoleAdmin); err != nil {
		t.Fatalf("set role: %v", err)
	}
	got, _ := svc.users.GetByID(context.Background(), user.ID)
	if got.Role != RoleAdmin {
		t.Errorf("role: %s", got.Role)
	}

	if err := svc.SetRole(context.Background(), user.ID, "bogus"); err == nil {
		t.Error("bogus role should fail")
	}
}

func TestService_ChangePassword(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)

	if err := svc.ChangePassword(context.Background(), user.ID, "P@ssw0rd!Strong", "NewP@ssw0rd!Strong"); err != nil {
		t.Fatalf("change: %v", err)
	}
	if _, err := svc.Login(context.Background(), "u@x.com", "P@ssw0rd!Strong", "", "", ""); err == nil {
		t.Error("old password should not login after change")
	}
	if _, err := svc.Login(context.Background(), "u@x.com", "NewP@ssw0rd!Strong", "", "", ""); err != nil {
		t.Errorf("new password login: %v", err)
	}
}

func TestService_ChangePassword_WrongOld(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	if err := svc.ChangePassword(context.Background(), user.ID, "wrong", "NewP@ssw0rd!Strong"); !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("expected ErrInvalidCredential, got %v", err)
	}
}

func TestService_NoAPIKeyRepo(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	svc := NewService(NewMemUserRepo(), NewRedisSessionStore(rdb, ""), Options{
		PasswordParams: testPwParams(), MinPasswordStrength: -1,
	})
	if _, _, err := svc.CreateAPIKey(context.Background(), "u1", CreateAPIKeyInput{Name: "k"}); !errors.Is(err, ErrAPIKeyNotEnabled) {
		t.Errorf("expected ErrAPIKeyNotEnabled, got %v", err)
	}
}
