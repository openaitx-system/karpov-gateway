package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newSvcWithIPLimit 构造一个带"一 IP 一账号"配置的 Service，便于多个测试共用。
//
// 关闭密码强度评分（MinPasswordStrength = -1），让测试只聚焦 IP 限制逻辑。
func newSvcWithIPLimit(t *testing.T, allow []string) (*Service, *MemUserRepo) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	repo := NewMemUserRepo()
	sess := NewRedisSessionStore(rdb, "")
	svc := NewService(repo, sess, Options{
		PasswordParams:      testPwParams(),
		SessionTTL:          time.Hour,
		MinPasswordStrength: -1,
		IPRegisterLimit:     true,
		IPRegisterAllowList: allow,
	})
	return svc, repo
}

// 一个 IP 注册一个账号 → 第二次同 IP 注册被拒。
func TestRegister_IPLimit_RejectsSecondAccount(t *testing.T) {
	svc, repo := newSvcWithIPLimit(t, nil)
	ctx := context.Background()

	u1, err := svc.Register(ctx, "a@x.com", "pw", "203.0.113.7", "")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if u1.RegisterIP != "203.0.113.7" {
		t.Errorf("RegisterIP not persisted: %q", u1.RegisterIP)
	}
	if got, _ := repo.CountByRegisterIP(ctx, "203.0.113.7"); got != 1 {
		t.Errorf("count after first register = %d, want 1", got)
	}

	_, err = svc.Register(ctx, "b@x.com", "pw", "203.0.113.7", "")
	if !errors.Is(err, ErrIPAlreadyRegistered) {
		t.Errorf("expected ErrIPAlreadyRegistered, got %v", err)
	}

	// 不同 IP 仍然放行
	if _, err := svc.Register(ctx, "c@x.com", "pw", "203.0.113.8", ""); err != nil {
		t.Errorf("different IP should pass: %v", err)
	}
}

// 限制关闭时同 IP 不去重。
func TestRegister_IPLimit_DisabledByDefault(t *testing.T) {
	svc, _, _, _ := newAuthSvc(t) // 默认 IPRegisterLimit = false
	ctx := context.Background()

	if _, err := svc.Register(ctx, "a@x.com", "pw", "203.0.113.7", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.Register(ctx, "b@x.com", "pw", "203.0.113.7", ""); err != nil {
		t.Errorf("limit disabled, second should pass: %v", err)
	}
	// 关闭时不应写入 register_ip
	u, _ := svc.users.GetByEmail(ctx, "a@x.com")
	if u.RegisterIP != "" {
		t.Errorf("limit off ⇒ RegisterIP should stay empty, got %q", u.RegisterIP)
	}
}

// 空 IP 跳过去重（兜底：解析失败 / 反代未传 X-Forwarded-For 时不阻断注册）。
func TestRegister_IPLimit_EmptyIPSkipped(t *testing.T) {
	svc, _ := newSvcWithIPLimit(t, nil)
	ctx := context.Background()

	if _, err := svc.Register(ctx, "a@x.com", "pw", "", ""); err != nil {
		t.Fatalf("empty IP first: %v", err)
	}
	if _, err := svc.Register(ctx, "b@x.com", "pw", "", ""); err != nil {
		t.Errorf("empty IP should not block: %v", err)
	}
}

// 命中 CIDR 白名单的 IP 可以重复注册，且不会写入 register_ip。
func TestRegister_IPLimit_AllowlistCIDRBypass(t *testing.T) {
	svc, repo := newSvcWithIPLimit(t, []string{"10.0.0.0/8", "127.0.0.1"})
	ctx := context.Background()

	// 10.x.x.x 命中 CIDR
	u1, err := svc.Register(ctx, "a@x.com", "pw", "10.1.2.3", "")
	if err != nil {
		t.Fatalf("allowlisted CIDR first: %v", err)
	}
	if u1.RegisterIP != "" {
		t.Errorf("allowlisted IP must not be persisted, got %q", u1.RegisterIP)
	}
	if _, err := svc.Register(ctx, "b@x.com", "pw", "10.1.2.3", ""); err != nil {
		t.Errorf("allowlisted CIDR second: %v", err)
	}

	// 单 IP 命中
	if _, err := svc.Register(ctx, "c@x.com", "pw", "127.0.0.1", ""); err != nil {
		t.Fatalf("allowlisted single first: %v", err)
	}
	if _, err := svc.Register(ctx, "d@x.com", "pw", "127.0.0.1", ""); err != nil {
		t.Errorf("allowlisted single second: %v", err)
	}

	// 白名单外的 IP 仍然受限
	if _, err := svc.Register(ctx, "e@x.com", "pw", "203.0.113.7", ""); err != nil {
		t.Fatalf("public IP first: %v", err)
	}
	_, err = svc.Register(ctx, "f@x.com", "pw", "203.0.113.7", "")
	if !errors.Is(err, ErrIPAlreadyRegistered) {
		t.Errorf("non-allowlisted dup should reject, got %v", err)
	}

	// 白名单 IP 不入库 ⇒ Count = 0
	if got, _ := repo.CountByRegisterIP(ctx, "10.1.2.3"); got != 0 {
		t.Errorf("allowlisted CIDR count = %d, want 0 (must not enter index)", got)
	}
	if got, _ := repo.CountByRegisterIP(ctx, "127.0.0.1"); got != 0 {
		t.Errorf("allowlisted single count = %d, want 0", got)
	}
}

// 非法的白名单条目会被 ParseIPRegisterAllowList 静默丢弃 + warn，不影响 Service 启动。
// 这里只验证 Service 仍然按"普通 IP 受限"处理，没有 panic。
func TestRegister_IPLimit_BadAllowlistEntriesIgnored(t *testing.T) {
	svc, _ := newSvcWithIPLimit(t, []string{"not-an-ip", "256.256.256.256/8", "1.2.3.4"})
	ctx := context.Background()

	// 1.2.3.4 是合法白名单
	if _, err := svc.Register(ctx, "a@x.com", "pw", "1.2.3.4", ""); err != nil {
		t.Fatalf("good allowlist entry: %v", err)
	}
	if _, err := svc.Register(ctx, "b@x.com", "pw", "1.2.3.4", ""); err != nil {
		t.Errorf("good allowlist should bypass: %v", err)
	}
	// 普通 IP 受限
	if _, err := svc.Register(ctx, "c@x.com", "pw", "8.8.8.8", ""); err != nil {
		t.Fatalf("normal IP first: %v", err)
	}
	if _, err := svc.Register(ctx, "d@x.com", "pw", "8.8.8.8", ""); !errors.Is(err, ErrIPAlreadyRegistered) {
		t.Errorf("normal IP second should reject, got %v", err)
	}
}

// 反查发生在 GetByEmail 之后：重复 email 仍然得到 ErrUserExists（不是 ErrIPAlreadyRegistered），
// 防止"用相同 email 探测某 IP 是否注册过"侧信道。
func TestRegister_IPLimit_DuplicateEmailNotMaskedByIP(t *testing.T) {
	svc, _ := newSvcWithIPLimit(t, nil)
	ctx := context.Background()

	if _, err := svc.Register(ctx, "a@x.com", "pw", "10.0.0.1", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	// 同 email + 同 IP：应当先看到 email 重复
	_, err := svc.Register(ctx, "a@x.com", "pw", "10.0.0.1", "")
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("duplicate email should beat IP check, got %v", err)
	}
}

// IPv6 地址应该按 net.IP 等价比较：缩写形式 vs 完整形式都判同一个 IP。
func TestRegister_IPLimit_IPv6Normalization(t *testing.T) {
	svc, _ := newSvcWithIPLimit(t, []string{"2001:db8::/32"})
	ctx := context.Background()

	if _, err := svc.Register(ctx, "a@x.com", "pw", "2001:db8::1", ""); err != nil {
		t.Fatalf("ipv6 first allowlisted: %v", err)
	}
	// 同 CIDR 内不同地址，仍命中白名单
	if _, err := svc.Register(ctx, "b@x.com", "pw", "2001:db8:abcd::42", ""); err != nil {
		t.Errorf("ipv6 CIDR allowlist second: %v", err)
	}
	// CIDR 外的 IPv6 受限
	if _, err := svc.Register(ctx, "c@x.com", "pw", "2001:db9::1", ""); err != nil {
		t.Fatalf("ipv6 outside CIDR first: %v", err)
	}
	if _, err := svc.Register(ctx, "d@x.com", "pw", "2001:db9::1", ""); !errors.Is(err, ErrIPAlreadyRegistered) {
		t.Errorf("ipv6 outside CIDR dup should reject, got %v", err)
	}
}

// CountByRegisterIP 自身：MemUserRepo 应正确按 IP 计数 + 空 IP 短路返回 0。
func TestMemUserRepo_CountByRegisterIP(t *testing.T) {
	repo := NewMemUserRepo()
	ctx := context.Background()

	_ = repo.Create(ctx, &User{Email: "a", PasswordHash: "x", RegisterIP: "1.1.1.1"})
	_ = repo.Create(ctx, &User{Email: "b", PasswordHash: "x", RegisterIP: "1.1.1.1"})
	_ = repo.Create(ctx, &User{Email: "c", PasswordHash: "x", RegisterIP: "2.2.2.2"})
	_ = repo.Create(ctx, &User{Email: "d", PasswordHash: "x"}) // RegisterIP 空

	got, err := repo.CountByRegisterIP(ctx, "1.1.1.1")
	if err != nil || got != 2 {
		t.Errorf("count 1.1.1.1 = %d %v, want 2", got, err)
	}
	got, _ = repo.CountByRegisterIP(ctx, "2.2.2.2")
	if got != 1 {
		t.Errorf("count 2.2.2.2 = %d, want 1", got)
	}
	got, _ = repo.CountByRegisterIP(ctx, "9.9.9.9")
	if got != 0 {
		t.Errorf("count missing = %d, want 0", got)
	}
	// 空 IP 应短路返回 0 而不是计数 RegisterIP=="" 的所有用户
	got, _ = repo.CountByRegisterIP(ctx, "")
	if got != 0 {
		t.Errorf("count empty = %d, want 0 (must short-circuit)", got)
	}
}
