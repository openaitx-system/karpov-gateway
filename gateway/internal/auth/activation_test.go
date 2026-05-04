package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/MiChongs/QQMusicApi/gateway/internal/email"
)

// activationDeps 把激活流要的依赖打包，便于测试每条分支。
type activationDeps struct {
	svc    *Service
	users  *MemUserRepo
	sender *email.MockSender
	tokens *MemActivationTokenStore
	verify *MemEmailVerificationStore
}

func newActivationSvc(t *testing.T, override Options) *activationDeps {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	users := NewMemUserRepo()
	sessions := NewRedisSessionStore(rdb, "")
	sender := email.NewMockSender("noreply@x.com")
	tokens := NewMemActivationTokenStore()
	verify := NewMemEmailVerificationStore()

	// 用真实墙钟而非冻结时钟：MemActivationTokenStore 在 GetByToken 里用 time.Now()
	// 判过期；如果 svc.clock 冻结到一个未来时刻，token 永远不会被判为过期。
	// 测试想覆盖"过期"分支时只需把 ActivationTTL 设为 1ms + time.Sleep。
	clockFn := func() time.Time { return time.Now() }

	opts := override
	if opts.PasswordParams.KeyLen == 0 {
		opts.PasswordParams = testPwParams()
	}
	if opts.MinPasswordStrength == 0 {
		opts.MinPasswordStrength = -1
	}
	opts.Clock = clockFn
	opts.EmailSender = sender
	opts.EmailVerification = verify
	opts.ActivationTokens = tokens
	if opts.ActivationBaseURL == "" {
		opts.ActivationBaseURL = "https://app.example.com/activate"
	}
	if opts.ActivationTTL == 0 {
		opts.ActivationTTL = 24 * time.Hour
	}

	svc := NewService(users, sessions, opts)
	return &activationDeps{svc: svc, users: users, sender: sender, tokens: tokens, verify: verify}
}

// 启用激活流时：Register 把账号写为 pending_email + 发激活邮件。
func TestRegister_ActivationRequired_PendingAndEmail(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true})
	ctx := context.Background()
	u, err := d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.Status != "pending_email" {
		t.Errorf("status = %q, want pending_email", u.Status)
	}
	last, ok := d.sender.Last()
	if !ok {
		t.Fatalf("activation email not sent")
	}
	if !strings.Contains(last.HTMLBody, "https://app.example.com/activate?token=") {
		t.Errorf("activation URL missing or malformed: %q", last.HTMLBody)
	}
	if !strings.Contains(last.Subject, "激活") {
		t.Errorf("subject not Chinese-activation: %q", last.Subject)
	}
}

// 不启用激活流时：账号 active + 不发邮件。
func TestRegister_ActivationDisabled_NoEmail(t *testing.T) {
	d := newActivationSvc(t, Options{ /* ActivationRequired: false */ })
	ctx := context.Background()
	u, err := d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.Status != "active" {
		t.Errorf("status = %q, want active", u.Status)
	}
	if _, ok := d.sender.Last(); ok {
		t.Errorf("activation email should not be sent")
	}
}

// ActivateAccount 正常路径：pending → active；token 一次性消费。
func TestActivateAccount_HappyPath(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true})
	ctx := context.Background()
	if _, err := d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", ""); err != nil {
		t.Fatalf("register: %v", err)
	}
	last, _ := d.sender.Last()
	token := extractTokenFromURL(t, last.HTMLBody)

	u, err := d.svc.ActivateAccount(ctx, token)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if u.Status != "active" {
		t.Errorf("status = %q after activate", u.Status)
	}

	// 同 token 再用 → ErrActivationTokenUsed
	if _, err := d.svc.ActivateAccount(ctx, token); !errors.Is(err, ErrActivationTokenUsed) {
		t.Errorf("second use should be ErrActivationTokenUsed, got %v", err)
	}
}

// 无效 token → ErrActivationTokenInvalid。
func TestActivateAccount_InvalidToken(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true})
	if _, err := d.svc.ActivateAccount(context.Background(), "totally-fake-token"); !errors.Is(err, ErrActivationTokenInvalid) {
		t.Errorf("expected ErrActivationTokenInvalid, got %v", err)
	}
}

// 过期 token → ErrActivationTokenExpired。
func TestActivateAccount_Expired(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true, ActivationTTL: time.Millisecond})
	ctx := context.Background()
	_, _ = d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	last, _ := d.sender.Last()
	token := extractTokenFromURL(t, last.HTMLBody)
	time.Sleep(10 * time.Millisecond) // 过 TTL
	if _, err := d.svc.ActivateAccount(ctx, token); !errors.Is(err, ErrActivationTokenExpired) {
		t.Errorf("expected ErrActivationTokenExpired, got %v", err)
	}
}

// 已 active 账号再用合法 token → ErrAccountAlreadyActive。
func TestActivateAccount_AlreadyActive(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true})
	ctx := context.Background()
	_, _ = d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	last, _ := d.sender.Last()
	token := extractTokenFromURL(t, last.HTMLBody)

	// 手动把账号置 active（模拟 admin 直接切的场景）
	u, _ := d.users.GetByEmail(ctx, "u@x.com")
	u.Status = "active"
	_ = d.users.Update(ctx, u)

	if _, err := d.svc.ActivateAccount(ctx, token); !errors.Is(err, ErrAccountAlreadyActive) {
		t.Errorf("expected ErrAccountAlreadyActive, got %v", err)
	}
}

// pending_email 账号登录 → ErrAccountNotActivated（密码正确）。
func TestLogin_PendingEmail_Rejected(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true})
	ctx := context.Background()
	_, _ = d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	if _, err := d.svc.Login(ctx, "u@x.com", "P@ssw0rd!Strong", "", "", ""); !errors.Is(err, ErrAccountNotActivated) {
		t.Errorf("expected ErrAccountNotActivated, got %v", err)
	}
	// 密码错时应当先返回 InvalidCredential（不暴露 pending 状态）
	if _, err := d.svc.Login(ctx, "u@x.com", "wrong", "", "", ""); !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("wrong password should beat pending check, got %v", err)
	}
}

// ResendActivation 静默成功：邮箱不存在 → 返回 nil 不报错。
func TestResendActivation_SilentForUnknownEmail(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true})
	if err := d.svc.ResendActivation(context.Background(), "nobody@x.com", ""); err != nil {
		t.Errorf("unknown email should be silent success, got %v", err)
	}
	if _, ok := d.sender.Last(); ok {
		t.Errorf("no email should be sent for unknown address")
	}
}

// ResendActivation 静默成功：邮箱已激活 → 返回 nil 不报错（同样防探测）。
func TestResendActivation_SilentForAlreadyActive(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: false}) // 注册即 active
	ctx := context.Background()
	_, _ = d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	d.sender.Reset()

	if err := d.svc.ResendActivation(ctx, "u@x.com", ""); err != nil {
		t.Errorf("already-active email should be silent success, got %v", err)
	}
	if _, ok := d.sender.Last(); ok {
		t.Errorf("no email for already-active user")
	}
}

// ResendActivation 命中 pending 用户：会发邮件 + cooldown 写入。
func TestResendActivation_PendingTriggersEmail(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true, EmailCodeCooldown: -1}) // 关 cooldown 专注路径
	ctx := context.Background()
	_, _ = d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	d.sender.Reset()

	if err := d.svc.ResendActivation(ctx, "u@x.com", ""); err != nil {
		t.Fatalf("resend: %v", err)
	}
	last, ok := d.sender.Last()
	if !ok || last.To != "u@x.com" {
		t.Errorf("email not resent: %+v", last)
	}
}

// ResendActivation cooldown：默认 60s 内重复发拒绝。
func TestResendActivation_Cooldown(t *testing.T) {
	d := newActivationSvc(t, Options{ActivationRequired: true})
	ctx := context.Background()
	_, _ = d.svc.Register(ctx, "u@x.com", "P@ssw0rd!Strong", "", "")
	if err := d.svc.ResendActivation(ctx, "u@x.com", ""); err != nil {
		t.Fatalf("first resend: %v", err)
	}
	if err := d.svc.ResendActivation(ctx, "u@x.com", ""); !errors.Is(err, ErrEmailCodeCooldown) {
		t.Errorf("second resend should hit cooldown, got %v", err)
	}
}

// AccountActivationStatus 反映启动期 demote 行为。
func TestAccountActivationStatus_DemotedWhenMisconfigured(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	users := NewMemUserRepo()
	sessions := NewRedisSessionStore(rdb, "")
	svc := NewService(users, sessions, Options{
		PasswordParams:      testPwParams(),
		MinPasswordStrength: -1,
		// 故意不传 EmailSender / ActivationTokens / BaseURL；启动期应当 demote
		ActivationRequired: true,
	})
	st := svc.AccountActivationStatus()
	if st.Enabled || st.Required {
		t.Errorf("misconfigured activation should be disabled, got %+v", st)
	}
	// Register 也应当走 active 路径而非 pending_email
	u, err := svc.Register(context.Background(), "u@x.com", "P@ssw0rd!Strong", "", "")
	if err != nil || u.Status != "active" {
		t.Errorf("misconfig should fall back to active, got status=%q err=%v", u.Status, err)
	}
}

// buildActivationURL 三种 base 形态。
func TestBuildActivationURL(t *testing.T) {
	cases := []struct {
		base, token, want string
	}{
		{"https://app/activate", "abc", "https://app/activate?token=abc"},
		{"https://app/activate?lang=zh", "abc", "https://app/activate?lang=zh&token=abc"},
		{"https://app/activate?token=", "abc", "https://app/activate?token=abc"},
	}
	for _, c := range cases {
		if got := buildActivationURL(c.base, c.token); got != c.want {
			t.Errorf("base=%q ⇒ %q, want %q", c.base, got, c.want)
		}
	}
}

// extractTokenFromURL 从邮件 HTML 里取 ?token=xxx 的值；测试辅助用。
func extractTokenFromURL(t *testing.T, body string) string {
	t.Helper()
	const marker = "?token="
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("token not in body: %q", body)
	}
	rest := body[i+len(marker):]
	for j, r := range rest {
		// token 以非 base64url 字符截断（"<", " ", "&" 等）
		if r == '"' || r == '<' || r == ' ' || r == '\n' || r == '\r' || r == '&' {
			return rest[:j]
		}
	}
	return rest
}
