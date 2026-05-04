package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// 用低成本 PasswordParams 加速测试（生产用 DefaultPasswordParams）。
func testPwParams() PasswordParams {
	return PasswordParams{TimeCost: 1, MemoryCost: 8 * 1024, Threads: 1, SaltLen: 16, KeyLen: 32}
}

func newAuthSvc(t *testing.T) (*Service, *MemUserRepo, *RedisSessionStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	repo := NewMemUserRepo()
	sess := NewRedisSessionStore(rdb, "")
	svc := NewService(repo, sess, Options{
		PasswordParams:      testPwParams(),
		SessionTTL:          1 * time.Hour,
		MinPasswordStrength: -1, // 单元测试关闭强度评分；强度由 strength_test.go 单独覆盖
	})
	return svc, repo, sess, mr
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("hunter2", testPwParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("phc prefix: %q", hash)
	}
	ok, err := VerifyPassword("hunter2", hash)
	if err != nil || !ok {
		t.Errorf("correct password rejected: ok=%v err=%v", ok, err)
	}
	ok, _ = VerifyPassword("wrong", hash)
	if ok {
		t.Errorf("wrong password accepted")
	}
}

func TestRegister_And_Login(t *testing.T) {
	svc, _, _, _ := newAuthSvc(t)
	ctx := context.Background()
	u, err := svc.Register(ctx, "a@b.c", "password123", "", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.ID == "" || u.Status != "active" {
		t.Errorf("user: %+v", u)
	}
	// 重复注册
	_, err = svc.Register(ctx, "a@b.c", "x", "", "")
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("duplicate register: %v", err)
	}
	// 登录成功
	res, err := svc.Login(ctx, "a@b.c", "password123", "", "1.2.3.4", "test-agent")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.User == nil || res.User.ID != u.ID {
		t.Errorf("user mismatch: %+v", res.User)
	}
	if res.Session == nil || res.Session.SID == "" || res.Session.IP != "1.2.3.4" {
		t.Errorf("session: %+v", res.Session)
	}
	if res.Challenge != "" {
		t.Errorf("unexpected challenge for non-2FA user")
	}
}

func TestLogin_BadPassword(t *testing.T) {
	svc, _, _, _ := newAuthSvc(t)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.c", "good", "", "")
	_, err := svc.Login(ctx, "a@b.c", "bad", "", "", "")
	if !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("bad password: %v", err)
	}
	_, err = svc.Login(ctx, "nobody@x.y", "any", "", "", "")
	if !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("non-existent: %v", err)
	}
}

func TestSession_Lifecycle(t *testing.T) {
	svc, _, _, _ := newAuthSvc(t)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.c", "pw", "", "")
	res, _ := svc.Login(ctx, "a@b.c", "pw", "", "", "")
	sess := res.Session

	u, sess2, err := svc.VerifySession(ctx, sess.SID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if u.Email != "a@b.c" || sess2.SID != sess.SID {
		t.Errorf("verify mismatch")
	}
	// Logout
	if err := svc.Logout(ctx, sess.SID); err != nil {
		t.Fatalf("logout: %v", err)
	}
	_, _, err = svc.VerifySession(ctx, sess.SID)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound: %v", err)
	}
}

func TestLogin_FixationProtection(t *testing.T) {
	// 同一用户两次 Login 应得到不同 SID（固化重生成）
	svc, _, _, _ := newAuthSvc(t)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.c", "pw", "", "")
	r1, _ := svc.Login(ctx, "a@b.c", "pw", "", "", "")
	r2, _ := svc.Login(ctx, "a@b.c", "pw", "", "", "")
	if r1.Session.SID == r2.Session.SID {
		t.Errorf("session fixation: SID reused")
	}
}

func TestNewAPIKey_AndVerify(t *testing.T) {
	k, err := NewAPIKey(testPwParams())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(k.Plain, "mk_") {
		t.Errorf("prefix: %q", k.Plain)
	}
	if len(k.Plain) < 30 {
		t.Errorf("key too short: %d", len(k.Plain))
	}
	if k.Prefix != k.Plain[:8] {
		t.Errorf("prefix mismatch")
	}
	ok, err := VerifyAPIKey(k.Plain, k.Hash)
	if err != nil || !ok {
		t.Errorf("verify: ok=%v err=%v", ok, err)
	}
	ok, _ = VerifyAPIKey("mk_wrong_value_xxxxxxxxxxxxxxxxxxx", k.Hash)
	if ok {
		t.Errorf("wrong key accepted")
	}
	// Malformed 缺前缀
	_, err = VerifyAPIKey("nomk_abc", k.Hash)
	if !errors.Is(err, ErrAPIKeyMalformed) {
		t.Errorf("malformed: %v", err)
	}
}

func TestTOTP_GenerateAndVerify(t *testing.T) {
	secret, url, err := TOTPGenerate("QQMusicGW", "a@b.c")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if secret == "" || !strings.HasPrefix(url, "otpauth://") {
		t.Errorf("totp gen: secret=%q url=%q", secret, url)
	}
	// 用 totp.GenerateCode 拿当前码并校验
	// 不导入这个函数，直接调用 pquerna/otp/totp 的 ValidateCustom 链路
	// 通过 TOTPVerify 的 Skew=1 容忍机制 — 这里仅做 negative test 避免时间敏感
	if TOTPVerify(secret, "000000") {
		t.Errorf("wrong code accepted")
	}
}

func TestTOTPReplayBlocker(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	b := NewTOTPReplayBlocker(rdb, "")
	ctx := context.Background()

	ok, err := b.CheckAndMark(ctx, "u1", "123456")
	if err != nil || !ok {
		t.Errorf("first use: ok=%v err=%v", ok, err)
	}
	ok, _ = b.CheckAndMark(ctx, "u1", "123456")
	if ok {
		t.Errorf("replay should be blocked")
	}
	// 不同用户同 code 不互相干扰
	ok, _ = b.CheckAndMark(ctx, "u2", "123456")
	if !ok {
		t.Errorf("different user blocked")
	}
}

func TestNewSID_Uniqueness(t *testing.T) {
	a, _ := NewSID()
	b, _ := NewSID()
	if a == b {
		t.Errorf("SID collision")
	}
	if len(a) < 40 {
		t.Errorf("SID too short: %d", len(a))
	}
}
