package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/MiChongs/QQMusicApi/gateway/internal/email"
)

// emailTestDeps 把"邮箱子系统下注册"用的依赖打包；测试通过定制 opts 覆盖各分支。
type emailTestDeps struct {
	svc      *Service
	users    *MemUserRepo
	sender   *email.MockSender
	verify   *MemEmailVerificationStore
	clockNow func() time.Time
}

func newEmailSvc(t *testing.T, opts Options) *emailTestDeps {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	users := NewMemUserRepo()
	sessions := NewRedisSessionStore(rdb, "")
	sender := email.NewMockSender("from@x.com")
	verify := NewMemEmailVerificationStore()

	clockTime := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clockFn := func() time.Time { return clockTime }

	if opts.PasswordParams.KeyLen == 0 {
		opts.PasswordParams = testPwParams()
	}
	if opts.MinPasswordStrength == 0 {
		opts.MinPasswordStrength = -1
	}
	opts.Clock = clockFn
	opts.EmailSender = sender
	opts.EmailVerification = verify

	svc := NewService(users, sessions, opts)
	return &emailTestDeps{
		svc: svc, users: users, sender: sender, verify: verify, clockNow: clockFn,
	}
}

// 域名白名单（精确）拒绝外部域名。
func TestRegister_EmailDomain_ExactAllow(t *testing.T) {
	d := newEmailSvc(t, Options{
		EmailAllowedDomains: []string{"qq.com"},
	})
	ctx := context.Background()
	if _, err := d.svc.Register(ctx, "u@gmail.com", "P@ssw0rd!Strong", "", ""); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Errorf("expected ErrEmailDomainNotAllowed for gmail, got %v", err)
	}
	if _, err := d.svc.Register(ctx, "u@qq.com", "P@ssw0rd!Strong", "", ""); err != nil {
		t.Errorf("qq.com should pass: %v", err)
	}
}

// 通配后缀 *.edu.cn 允许任意 .edu.cn 子域。
func TestRegister_EmailDomain_SuffixWildcard(t *testing.T) {
	d := newEmailSvc(t, Options{
		EmailAllowedDomains: []string{"*.edu.cn"},
	})
	ctx := context.Background()
	if _, err := d.svc.Register(ctx, "u@tsinghua.edu.cn", "P@ssw0rd!Strong", "", ""); err != nil {
		t.Errorf("edu.cn subdomain should pass: %v", err)
	}
	if _, err := d.svc.Register(ctx, "u@qq.com", "P@ssw0rd!Strong", "", ""); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Errorf("qq.com should be denied")
	}
}

// 黑名单优先：即便在白名单里，命中黑名单也拒。
func TestRegister_EmailDomain_BlocklistOverridesAllow(t *testing.T) {
	d := newEmailSvc(t, Options{
		EmailAllowedDomains: []string{"qq.com"},
		EmailBlockedDomains: []string{"qq.com"},
	})
	ctx := context.Background()
	if _, err := d.svc.Register(ctx, "u@qq.com", "P@ssw0rd!Strong", "", ""); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Errorf("blocklist must win, got %v", err)
	}
}

// emailRequired=true 时必须带 code。
func TestRegister_EmailCode_RequiredWhenEnabled(t *testing.T) {
	d := newEmailSvc(t, Options{EmailVerificationRequired: true})
	ctx := context.Background()

	// 没带 code → ErrEmailCodeRequired
	if _, err := d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", ""); !errors.Is(err, ErrEmailCodeRequired) {
		t.Errorf("missing code should be required, got %v", err)
	}

	// 错误 code → ErrEmailCodeInvalid
	_ = d.verify.PutCode(ctx, "u@x.com", "111111", time.Minute)
	if _, err := d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "999999"); !errors.Is(err, ErrEmailCodeInvalid) {
		t.Errorf("wrong code should be invalid, got %v", err)
	}

	// 正确 code → 通过；store 里的 code 应被消费
	if _, err := d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "111111"); err != nil {
		t.Fatalf("correct code register failed: %v", err)
	}
	if _, err := d.verify.GetCode(ctx, "u@x.com"); !errors.Is(err, ErrEmailCodeMissing) {
		t.Errorf("code should be deleted after consume, got %v", err)
	}
}

// emailRequired=false 时（默认）允许不带 code 注册。
func TestRegister_EmailCode_OptionalWhenDisabled(t *testing.T) {
	d := newEmailSvc(t, Options{}) // EmailVerificationRequired = false
	ctx := context.Background()
	if _, err := d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", ""); err != nil {
		t.Errorf("optional mode no-code should pass: %v", err)
	}
}

// SendEmailVerification 正常路径：发邮件 + 内存里有 code + cooldown 已写入。
func TestSendEmailVerification_HappyPath(t *testing.T) {
	d := newEmailSvc(t, Options{})
	ctx := context.Background()
	if err := d.svc.SendEmailVerification(ctx, "u@x.com", "1.2.3.4"); err != nil {
		t.Fatalf("send: %v", err)
	}
	last, ok := d.sender.Last()
	if !ok || last.To != "u@x.com" {
		t.Errorf("sender did not receive: %+v", last)
	}
	if _, err := d.verify.GetCode(ctx, "u@x.com"); err != nil {
		t.Errorf("code not stored: %v", err)
	}
	t0, _ := d.verify.GetLastSentAt(ctx, "u@x.com")
	if t0.IsZero() {
		t.Errorf("MarkSentAt not called")
	}
}

// 域名规则同样作用于 SendEmailVerification。
func TestSendEmailVerification_DomainRule(t *testing.T) {
	d := newEmailSvc(t, Options{
		EmailAllowedDomains: []string{"qq.com"},
	})
	if err := d.svc.SendEmailVerification(context.Background(), "u@gmail.com", ""); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Errorf("non-allowed domain should be rejected, got %v", err)
	}
}

// 子系统未配置（sender / verify 均 nil）时 Send 直接返回 ErrEmailNotConfigured。
func TestSendEmailVerification_NotConfigured(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	users := NewMemUserRepo()
	sessions := NewRedisSessionStore(rdb, "")
	svc := NewService(users, sessions, Options{
		PasswordParams:      testPwParams(),
		MinPasswordStrength: -1,
	})
	if err := svc.SendEmailVerification(context.Background(), "u@x.com", ""); !errors.Is(err, ErrEmailNotConfigured) {
		t.Errorf("expected ErrEmailNotConfigured, got %v", err)
	}
}

// 冷却期内重复 Send 应返回 ErrEmailCodeCooldown。
func TestSendEmailVerification_Cooldown(t *testing.T) {
	d := newEmailSvc(t, Options{
		EmailCodeCooldown: 60 * time.Second,
	})
	ctx := context.Background()
	if err := d.svc.SendEmailVerification(ctx, "u@x.com", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	// 同一 clock 下立即再发 → 冷却中
	if err := d.svc.SendEmailVerification(ctx, "u@x.com", ""); !errors.Is(err, ErrEmailCodeCooldown) {
		t.Errorf("cooldown should kick in, got %v", err)
	}
}

// 每邮箱小时级上限：默认 5；前 5 次通过，第 6 次拒。
func TestSendEmailVerification_HourlyRateLimit(t *testing.T) {
	d := newEmailSvc(t, Options{
		EmailCodeCooldown:    -1, // 负值 = 显式关闭冷却，专注测限速
		EmailCodeHourlyLimit: 5,
	})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := d.svc.SendEmailVerification(ctx, "u@x.com", ""); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// 第 6 次：rate limit
	if err := d.svc.SendEmailVerification(ctx, "u@x.com", ""); !errors.Is(err, ErrEmailCodeRateLimit) {
		t.Errorf("6th should hit rate limit, got %v", err)
	}
}

// EmailVerificationStatus 反映当前配置：必填 + 域名列表 + cooldown。
func TestEmailVerificationStatus(t *testing.T) {
	d := newEmailSvc(t, Options{
		EmailVerificationRequired: true,
		EmailCodeTTL:              5 * time.Minute,
		EmailCodeCooldown:         30 * time.Second,
		EmailCodeHourlyLimit:      7,
		EmailAllowedDomains:       []string{"qq.com", "*.edu.cn"},
		EmailBlockedDomains:       []string{"badmail.io"},
		EmailAppName:              "MyApp",
	})
	st := d.svc.EmailVerificationStatus()
	if !st.Enabled || !st.Required {
		t.Errorf("enabled/required: %+v", st)
	}
	if st.CooldownSeconds != 30 || st.CodeTTLSeconds != 300 || st.HourlyLimitPerMail != 7 {
		t.Errorf("numbers wrong: %+v", st)
	}
	if len(st.AllowedDomains) != 2 {
		t.Errorf("allowed: %+v", st.AllowedDomains)
	}
	if len(st.BlockedDomains) != 1 {
		t.Errorf("blocked: %+v", st.BlockedDomains)
	}
	if st.AppName != "MyApp" {
		t.Errorf("appname: %s", st.AppName)
	}
}

// emailRequired=true 但子系统未配置 → 启动期降级为 false（warn），不阻塞注册。
func TestRegister_EmailRequired_DemotedWhenNotConfigured(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	users := NewMemUserRepo()
	sessions := NewRedisSessionStore(rdb, "")
	svc := NewService(users, sessions, Options{
		PasswordParams:            testPwParams(),
		MinPasswordStrength:       -1,
		EmailVerificationRequired: true, // 但 EmailSender / EmailVerification 都 nil
	})
	// Register 不应当因为 code 缺失而失败
	if _, err := svc.Register(context.Background(), "u@x.com", "P@ssw0rd!Strong", "", ""); err != nil {
		t.Errorf("required-but-misconfigured should demote, got %v", err)
	}
}
