package auth

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/MiChongs/QQMusicApi/gateway/internal/email"
)

// trimSpace + isValidIPOrCIDR 给 normalizeScopes/normalizeIPAllow 用。
func trimSpace(s string) string { return strings.TrimSpace(s) }

func isValidIPOrCIDR(s string) bool {
	if strings.Contains(s, "/") {
		_, _, err := net.ParseCIDR(s)
		return err == nil
	}
	return net.ParseIP(s) != nil
}

// PwnedChecker 是密码黑名单查询抽象（HIBP k-anonymity 等）。
//
// 实现见 internal/auth/hibp。Service 通过该接口而非强引用 hibp 包，
// 便于本地测试注入 mock。返回 (true, nil) 表示已被泄露应拒绝；网络错误
// 调用方决定 fail-open 或 fail-closed（Service.Register 选 fail-open）。
type PwnedChecker interface {
	IsPwned(ctx context.Context, password string) (bool, error)
}

// User 是用户领域模型（仅核心字段，PG 表 schema 见 migrations/auth/0001_init.up.sql）。
type User struct {
	ID           string
	Email        string
	PasswordHash string
	Status       string // "active" / "locked" / "banned"
	Role         string // RoleUser / RoleAdmin / RoleSuperAdmin；空 = 视为 RoleUser
	PlanID       string // "free" / "basic" / "pro" / "enterprise"；空 = 视为 "free"。新 API Key 继承此值。
	TOTPEnabled  bool
	TOTPSecret   string // 实际生产应当 AES-256-GCM 加密落库；当前明文（M16 时加密）
	CreatedAt    time.Time
	// RegisterIP 是注册时客户端 IP 快照（"一 IP 一账号"去重用）。
	// 与 last_login_ip 不同，本字段一旦写入只在管理员手工干预时变化。
	// 命中 IPRegisterAllowList 的注册请求不会写入此字段（保持 ""）。
	RegisterIP string
}

// EffectivePlanID 返回用户的有效套餐 ID（空字段视为 "free"）。
func (u *User) EffectivePlanID() string {
	if u == nil || u.PlanID == "" {
		return "free"
	}
	return u.PlanID
}

// 角色常量（RBAC）。
//
// 多副本部署时各 service 用 SessionMiddleware 注入的 X-User-Role 做授权决策；
// 角色分级：user < admin < superadmin（superadmin 仅做用户/角色管理）。
const (
	RoleUser       = "user"
	RoleAdmin      = "admin"
	RoleSuperAdmin = "superadmin"
)

// EffectiveRole 返回用户的有效角色（空字段视为 user）。
func (u *User) EffectiveRole() string {
	if u == nil || u.Role == "" {
		return RoleUser
	}
	return u.Role
}

// IsAdmin 是否拥有 admin 或 superadmin 权限。
func (u *User) IsAdmin() bool {
	r := u.EffectiveRole()
	return r == RoleAdmin || r == RoleSuperAdmin
}

// UserRepo 是用户存储抽象（PG 适配器实现同接口）。
type UserRepo interface {
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	Create(ctx context.Context, u *User) error
	Update(ctx context.Context, u *User) error
	// CountByRegisterIP 反查"该 IP 已注册多少账号"（"一 IP 一账号"去重用）。
	// ip 为空必须返回 0（视作"未知 IP"，不参与去重）。
	CountByRegisterIP(ctx context.Context, ip string) (int, error)
}

// ListUserFilter 是 ListUsers 的可选过滤参数。
//
// EmailLike：模糊匹配 email（不区分大小写，会自动加 % 包裹）；空 = 不过滤。
// Role / Status：精确匹配；空 = 不过滤。
// Limit / Offset：分页；Limit<=0 视为 50，最大 200。
type ListUserFilter struct {
	EmailLike string
	Role      string
	Status    string
	Limit     int
	Offset    int
}

// UserLister 是 UserRepo 的扩展接口；实现该接口表示支持列举用户（管理员后台用）。
//
// 不直接合到 UserRepo 上是为了避免影响所有现有实现/测试 mock；
// 任何 UserRepo 都可以可选地实现 UserLister。
type UserLister interface {
	ListUsers(ctx context.Context, filter ListUserFilter) (users []*User, total int, err error)
}

// Service 是 Auth 业务入口。
type Service struct {
	users       UserRepo
	sessions    SessionStore
	apiKeys     APIKeyRepo
	pwParams    PasswordParams
	clock       func() time.Time
	sessTTL     time.Duration
	minStrength PasswordStrength
	pwned       PwnedChecker
	log         *slog.Logger

	// IP 注册限制配置（"一 IP 一账号"）。
	// ipRegisterLimit = false ⇒ 完全跳过去重逻辑，clientIP 也不入库。
	// ipRegisterAllowCIDR / ipRegisterAllowSingle 是 IPRegisterAllowList 解析结果；
	// 命中任一规则的 IP 注册后 RegisterIP 留空，不进入反查 + 不被反查。
	ipRegisterLimit        bool
	ipRegisterAllowCIDR    []*net.IPNet
	ipRegisterAllowSingles []net.IP

	// 邮箱验证子系统（SMTP + 验证码）。
	// emailSender / emailVerify 均 nil ⇒ 子系统未启用：SendEmailVerification 返回
	// ErrEmailNotConfigured，Register 不要求 code（即便 emailRequired=true 也降级跳过）。
	emailSender    email.Sender
	emailVerify    EmailVerificationStore
	emailRenderer  *email.Renderer
	emailRequired  bool
	emailCodeTTL   time.Duration
	emailCooldown  time.Duration
	emailRLPerHour int
	emailDomain    *emailDomainPolicy
	emailAppName   string
	emailSupport   string

	// 账号激活子系统：用 ActivationTokenStore 存"未激活账号 → 一次性 token"映射。
	// activateRequired = true + sender + tokens + renderer 都齐 ⇒
	// Register 把账号写为 pending_email 并发激活邮件；用户点击邮件链接 → /activate?token=
	// → 前端 POST /v1/auth/email/verify → Service.ActivateAccount(token) → 落库 active。
	activateTokens   ActivationTokenStore
	activateRequired bool
	activateBaseURL  string
	activateTTL      time.Duration

	// 以下三项为可选——nil 时对应 TOTP RPC 返回 ErrTOTPNotEnabled，
	// 用户便可在没接 Redis 的开发环境正常用其它功能。生产链路必须都注入。
	totpIssuer    string
	totpPending   PendingTOTPStore
	totpChallenge TOTPChallengeStore
	totpReplay    *TOTPReplayBlocker
}

// Options 控制 Service 构造。
type Options struct {
	Clock          func() time.Time
	PasswordParams PasswordParams
	SessionTTL     time.Duration

	// MinPasswordStrength 是 Register 接受的最低密码强度。
	//
	// 默认 StrengthSafelyUnguess (3)；如果零值传入，将被替换为该默认值。
	// 测试可显式置 -1 关闭检查。
	MinPasswordStrength PasswordStrength

	// PwnedChecker 可选；非 nil 时 Register 会查询密码是否被 HIBP 收录。
	// 网络失败 fail-open（放行），仅用 logger 记录 warn。
	PwnedChecker PwnedChecker

	// APIKeyRepo 可选；非 nil 时启用 API Key CRUD（CreateAPIKey/...）。
	// 默认 nil = 那一组 RPC 返回 codes.Unimplemented。
	APIKeyRepo APIKeyRepo

	// TOTPIssuer 是 Authenticator App 顶部显示的发行方名（otpauth://issuer）。
	// 默认 "Karpov"。
	TOTPIssuer string

	// TOTPPendingStore / TOTPChallengeStore 可选；都不为空时启用完整 2FA 流程。
	TOTPPendingStore   PendingTOTPStore
	TOTPChallengeStore TOTPChallengeStore

	// TOTPReplayBlocker 可选；非 nil 时所有 TOTP 验证（登录第二步 / 关闭 / Confirm）
	// 都会先 CheckAndMark，防止同一 6 位码 30s 内重放。
	TOTPReplayBlocker *TOTPReplayBlocker

	// IPRegisterLimit 启用"一 IP 一账号"限制。默认 false（关闭）。
	// 开启后 Register 会按 clientIP 反查 register_ip = $1 已存在则返回 ErrIPAlreadyRegistered；
	// 命中 IPRegisterAllowList 的 IP 不写入 register_ip 也不参与反查。
	IPRegisterLimit bool

	// IPRegisterAllowList 是不参与去重的 IP/CIDR 白名单。
	// 典型场景：内网 / 出口网关 / NAT 地址（多个真实用户共享同一公网 IP）。
	// 元素接受："1.2.3.4" 或 "10.0.0.0/8"；解析失败的条目静默忽略并 warn 日志。
	IPRegisterAllowList []string

	// EmailSender 是 SMTP 发送器；nil ⇒ 邮箱验证子系统关闭。
	EmailSender email.Sender

	// EmailVerification 是验证码 + 频控存储；nil 时即使 EmailSender 非空，子系统也降级关闭。
	EmailVerification EmailVerificationStore

	// EmailVerificationRequired 注册时是否强制要求邮箱验证码：
	//   - true  + 子系统已配置 ⇒ Register 必填 code
	//   - true  + 子系统未配置 ⇒ Service 启动期 warn，运行期降级为 false
	//   - false ⇒ 注册仍可以 SendEmailVerification（提供给后续"邮箱激活"使用），但 code 非必填
	EmailVerificationRequired bool

	// EmailAllowedDomains 邮箱域名白名单（精确 "qq.com" 或后缀 "*.edu.cn"）；
	// 空 = 不限制。注册 + 发码两条路径都会校验。
	EmailAllowedDomains []string

	// EmailBlockedDomains 邮箱域名黑名单；典型场景：屏蔽一次性邮箱服务。
	// 黑名单优先于白名单：命中黑名单一律拒绝。
	EmailBlockedDomains []string

	// EmailCodeTTL 验证码有效期；默认 10 分钟。
	EmailCodeTTL time.Duration

	// EmailCodeCooldown 同一邮箱两次发送的最小间隔。
	// 0 = 默认 60s；负值（如 -1）= 显式关闭冷却（仅推荐测试 / 受信任内网）。
	EmailCodeCooldown time.Duration

	// EmailCodeHourlyLimit 同一邮箱 1 小时内的最大发送次数；默认 5。
	// 同时按 IP 维度记次（per-IP 上限是 EmailCodeHourlyLimit*4，避免单 NAT 多用户被打死）。
	EmailCodeHourlyLimit int

	// EmailAppName 邮件模板顶部品牌名；默认 TOTPIssuer 或 "Karpov"。
	EmailAppName string

	// EmailSupportAddress 邮件页脚展示的客服邮箱；空 = 隐藏。
	EmailSupportAddress string

	// ActivationTokens 是激活令牌存储；nil ⇒ 激活子系统关闭。
	// 推荐 NewPgActivationTokenStore，复用 auth.email_verifications 表。
	ActivationTokens ActivationTokenStore

	// ActivationRequired 注册新账号是否要求"邮件链接激活"才能登录：
	//   - true  + EmailSender + ActivationTokens 均非空 ⇒ 启用激活流，新账号写为 pending_email
	//   - true  + 任一未配置 ⇒ Service 启动期 warn，运行期降级为 false
	//   - false ⇒ 注册即 active，不发激活邮件
	ActivationRequired bool

	// ActivationBaseURL 是前端激活页的根 URL（不含 token），例如 https://app.example.com/activate
	// Service 在邮件正文里拼成 "{base}?token={tok}"。空字符串时启动期 warn，激活子系统降级关闭。
	ActivationBaseURL string

	// ActivationTTL 激活令牌有效期；默认 24h。
	ActivationTTL time.Duration

	// Logger 可选；nil 时用 slog.Default。仅用于 PwnedChecker 故障告警等
	// 非业务路径，不影响功能。
	Logger *slog.Logger
}

// NewService 构造 Auth Service。
func NewService(users UserRepo, sessions SessionStore, opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = func() time.Time { return time.Now().UTC() }
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 7 * 24 * time.Hour
	}
	if opts.PasswordParams.KeyLen == 0 {
		opts.PasswordParams = DefaultPasswordParams()
	}
	min := opts.MinPasswordStrength
	if min == 0 {
		min = StrengthSafelyUnguess
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	issuer := opts.TOTPIssuer
	if issuer == "" {
		issuer = "Karpov"
	}
	allowCIDR, allowSingles := parseIPRegisterAllowList(opts.IPRegisterAllowList, logger)

	// 邮箱子系统装配
	emailRequired := opts.EmailVerificationRequired
	if emailRequired && (opts.EmailSender == nil || opts.EmailVerification == nil) {
		logger.Warn("auth: email verification required but sender/store missing; demoting to optional")
		emailRequired = false
	}
	emailCodeTTL := opts.EmailCodeTTL
	if emailCodeTTL <= 0 {
		emailCodeTTL = 10 * time.Minute
	}
	// Cooldown 语义：负 = 显式关闭（测试 / 内网信任环境），零 = 取默认 60s，正值 = 按设。
	emailCooldown := opts.EmailCodeCooldown
	switch {
	case emailCooldown < 0:
		emailCooldown = 0
	case emailCooldown == 0:
		emailCooldown = 60 * time.Second
	}
	emailRL := opts.EmailCodeHourlyLimit
	if emailRL <= 0 {
		emailRL = 5
	}
	domainPolicy := newEmailDomainPolicy(opts.EmailAllowedDomains, opts.EmailBlockedDomains)
	appName := opts.EmailAppName
	if appName == "" {
		appName = issuer
	}
	var renderer *email.Renderer
	if opts.EmailSender != nil {
		// 渲染器可在 sender 已配但 verify 缺失时仍用于其他邮件类型（密码重置等，未来扩展）
		r, rerr := email.NewRenderer()
		if rerr != nil {
			logger.Error("auth: email renderer init failed; emails disabled", "err", rerr)
		} else {
			renderer = r
		}
	}

	// 激活子系统装配：sender + tokens + renderer + base URL 全到位才启用。
	activateRequired := opts.ActivationRequired
	if activateRequired {
		switch {
		case opts.EmailSender == nil:
			logger.Warn("auth: activation required but EmailSender missing; demoting to optional")
			activateRequired = false
		case opts.ActivationTokens == nil:
			logger.Warn("auth: activation required but ActivationTokens store missing; demoting to optional")
			activateRequired = false
		case renderer == nil:
			logger.Warn("auth: activation required but email renderer init failed; demoting to optional")
			activateRequired = false
		case strings.TrimSpace(opts.ActivationBaseURL) == "":
			logger.Warn("auth: activation required but ActivationBaseURL empty; demoting to optional")
			activateRequired = false
		}
	}
	activateTTL := opts.ActivationTTL
	if activateTTL <= 0 {
		activateTTL = 24 * time.Hour
	}

	return &Service{
		users:                  users,
		sessions:               sessions,
		apiKeys:                opts.APIKeyRepo,
		pwParams:               opts.PasswordParams,
		clock:                  opts.Clock,
		sessTTL:                opts.SessionTTL,
		minStrength:            min,
		pwned:                  opts.PwnedChecker,
		log:                    logger,
		ipRegisterLimit:        opts.IPRegisterLimit,
		ipRegisterAllowCIDR:    allowCIDR,
		ipRegisterAllowSingles: allowSingles,
		emailSender:            opts.EmailSender,
		emailVerify:            opts.EmailVerification,
		emailRenderer:          renderer,
		emailRequired:          emailRequired,
		emailCodeTTL:           emailCodeTTL,
		emailCooldown:          emailCooldown,
		emailRLPerHour:         emailRL,
		emailDomain:            domainPolicy,
		emailAppName:           appName,
		emailSupport:           opts.EmailSupportAddress,
		activateTokens:         opts.ActivationTokens,
		activateRequired:       activateRequired,
		activateBaseURL:        strings.TrimSpace(opts.ActivationBaseURL),
		activateTTL:            activateTTL,
		totpIssuer:             issuer,
		totpPending:            opts.TOTPPendingStore,
		totpChallenge:          opts.TOTPChallengeStore,
		totpReplay:             opts.TOTPReplayBlocker,
	}
}

// parseIPRegisterAllowList 把字符串列表解析为 (cidrs, singles)。
// 解析失败的条目用 logger.Warn 记录并跳过；不会因为单条非法配置阻断启动。
func parseIPRegisterAllowList(in []string, logger *slog.Logger) ([]*net.IPNet, []net.IP) {
	if len(in) == 0 {
		return nil, nil
	}
	cidrs := make([]*net.IPNet, 0, len(in))
	singles := make([]net.IP, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			_, network, err := net.ParseCIDR(s)
			if err != nil {
				logger.Warn("auth: skip bad register-ip allowlist CIDR", "rule", s, "err", err)
				continue
			}
			cidrs = append(cidrs, network)
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			logger.Warn("auth: skip bad register-ip allowlist IP", "rule", s)
			continue
		}
		singles = append(singles, ip)
	}
	return cidrs, singles
}

// isRegisterIPAllowlisted 判断 clientIP 是否在 IPRegisterAllowList 内。
// 空 IP / 解析失败一律返回 false（让上游 Register 走"未知 IP 不去重"路径）。
func (s *Service) isRegisterIPAllowlisted(clientIP string) bool {
	if clientIP == "" {
		return false
	}
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return false
	}
	for _, c := range s.ipRegisterAllowCIDR {
		if c.Contains(ip) {
			return true
		}
	}
	for _, single := range s.ipRegisterAllowSingles {
		if single.Equal(ip) {
			return true
		}
	}
	return false
}

// 已知错误。
var (
	ErrInvalidCredential = errors.New("auth: invalid credential")
	ErrUserExists        = errors.New("auth: user already exists")
	ErrUserNotFound      = errors.New("auth: user not found")
	ErrLocked            = errors.New("auth: account locked")
	// ErrIPAlreadyRegistered 表示注册被"一 IP 一账号"策略拦下：
	// 客户端 IP 已存在已注册账号且不在 IPRegisterAllowList 白名单内。
	ErrIPAlreadyRegistered = errors.New("auth: this IP already registered an account")
	// ErrPwnedPassword 表示密码命中 HIBP 黑名单。
	ErrPwnedPassword = errors.New("auth: password appears in known data breach")
	// ErrEmailNotConfigured 表示后端没配置 SMTP / 验证码存储；前端应当隐藏验证码输入。
	ErrEmailNotConfigured = errors.New("auth: email verification not configured")
	// ErrEmailCodeRequired 表示注册要求邮箱验证码但请求未带或为空。
	ErrEmailCodeRequired = errors.New("auth: email verification code required")
	// ErrEmailCodeInvalid 表示验证码错误（含已过期、已消费）。
	// 故意和"已过期"合并：让攻击者拿不到"码错误 vs 码已过期"区分，减少枚举攻击面。
	ErrEmailCodeInvalid = errors.New("auth: email verification code invalid or expired")
	// ErrEmailCodeCooldown 表示距上次发送未过冷却窗口；用于"发送验证码"路径。
	ErrEmailCodeCooldown = errors.New("auth: email code cooldown not elapsed")
	// ErrEmailCodeRateLimit 表示同一邮箱 / IP 在窗口内发送次数超限。
	ErrEmailCodeRateLimit = errors.New("auth: email code rate limit exceeded")
	// ErrTOTPNotEnabled 表示账号未开启 2FA 时调用了 Disable / Confirm 等。
	ErrTOTPNotEnabled = errors.New("auth: TOTP not enabled")
	// ErrTOTPAlreadyEnabled 用户重复点"启用"且已经启用。
	ErrTOTPAlreadyEnabled = errors.New("auth: TOTP already enabled")
	// ErrTOTPInvalid 6 位码错误 / 已重放 / 时钟漂移过大。
	ErrTOTPInvalid = errors.New("auth: invalid TOTP code")
	// ErrTOTPRequired 表示登录需要补 TOTP（Service 不会主动返回，由 LoginResult 表达）。
	ErrTOTPRequired = errors.New("auth: TOTP required")
	// ErrTOTPNotConfigured 表示当前部署没注入 TOTP store（未初始化 2FA 子系统）。
	ErrTOTPNotConfigured = errors.New("auth: TOTP subsystem not configured")
)

// EmailVerificationStatus 给前端读"当前邮箱子系统是否启用 / 域名规则"用。
type EmailVerificationStatus struct {
	Enabled            bool          // SMTP + 存储均已配置
	Required           bool          // 注册必填验证码
	CooldownSeconds    int           // 同一邮箱两次发送的最小间隔
	CodeTTLSeconds     int           // 验证码有效期
	HourlyLimitPerMail int           // 每邮箱每小时上限
	AllowedDomains     []string      // 显式允许；空 = 全部
	BlockedDomains     []string      // 显式阻断
	AppName            string        // 邮件品牌名（前端可同步显示）
	_                  time.Duration // 保留字段，未来加更多策略时不破坏 wire format
}

// EmailVerificationStatus 暴露当前子系统状态（前端 runtime config 用）。
//
// 当 emailSender 或 emailVerify 任一为空时 Enabled=false，调用方应当隐藏前端"发送验证码"控件。
func (s *Service) EmailVerificationStatus() EmailVerificationStatus {
	enabled := s.emailSender != nil && s.emailVerify != nil && s.emailRenderer != nil
	return EmailVerificationStatus{
		Enabled:            enabled,
		Required:           enabled && s.emailRequired,
		CooldownSeconds:    int(s.emailCooldown / time.Second),
		CodeTTLSeconds:     int(s.emailCodeTTL / time.Second),
		HourlyLimitPerMail: s.emailRLPerHour,
		AllowedDomains:     s.emailDomain.describeAllowed(),
		BlockedDomains:     s.emailDomain.describeBlocked(),
		AppName:            s.emailAppName,
	}
}

// AccountActivationStatus 是激活子系统的运行时状态。
type AccountActivationStatus struct {
	Enabled    bool   // sender + tokens + renderer + base url 全部就绪
	Required   bool   // 启用了"注册即 pending_email"流
	TTLSeconds int    // 链接有效期（秒）
	BaseURL    string // 前端激活页根 URL（前端可能不需要，但暴露便于排错）
}

// AccountActivationStatus 反射当前激活子系统配置。
func (s *Service) AccountActivationStatus() AccountActivationStatus {
	enabled := s.activateTokens != nil && s.emailSender != nil && s.emailRenderer != nil && s.activateBaseURL != ""
	return AccountActivationStatus{
		Enabled:    enabled,
		Required:   enabled && s.activateRequired,
		TTLSeconds: int(s.activateTTL / time.Second),
		BaseURL:    s.activateBaseURL,
	}
}

// SendEmailVerification 给指定邮箱发送 6 位数字验证码。
//
// 校验顺序（任一失败立即返回，不暴露多步串联信息）：
//   1. email 非空 + 域名规则 (allow/block)；
//   2. 子系统已配置（emailSender / emailVerify / renderer 均非空）；
//   3. cooldown：距上次发送 < EmailCodeCooldown ⇒ ErrEmailCodeCooldown；
//   4. per-email rate limit：1 小时内累计 > EmailCodeHourlyLimit ⇒ ErrEmailCodeRateLimit；
//   5. per-IP rate limit（IP 非空时才记）：上限 = EmailCodeHourlyLimit*4。
//   6. 生成 code → PutCode → MarkSentAt → renderer → sender.Send；任意一步失败返回。
//
// 安全考虑：发送失败时不删除 PutCode 写入的码（防止"用 SMTP 故障刷码空间"），但 MarkSentAt
// 已写入，cooldown 仍生效；下次同邮箱在 60s 内再发会被冷却拒绝，攻击窗口可控。
func (s *Service) SendEmailVerification(ctx context.Context, emailAddr, clientIP string) error {
	emailAddr = strings.TrimSpace(emailAddr)
	if emailAddr == "" {
		return ErrInvalidCredential
	}
	if !s.emailDomain.allows(emailAddr) {
		return ErrEmailDomainNotAllowed
	}
	if s.emailSender == nil || s.emailVerify == nil || s.emailRenderer == nil {
		return ErrEmailNotConfigured
	}
	now := s.clock()

	// cooldown
	if last, err := s.emailVerify.GetLastSentAt(ctx, emailAddr); err == nil && !last.IsZero() {
		if now.Sub(last) < s.emailCooldown {
			return ErrEmailCodeCooldown
		}
	}

	// per-email rate limit（小时窗口）
	if n, err := s.emailVerify.IncrSendCount(ctx, "email:"+strings.ToLower(emailAddr), time.Hour); err == nil {
		if n > s.emailRLPerHour {
			return ErrEmailCodeRateLimit
		}
	}
	// per-IP rate limit（同 IP 1 小时上限放宽到 emailRLPerHour*4，应对家庭 NAT）
	if clientIP != "" {
		ipLimit := s.emailRLPerHour * 4
		if ipLimit < 1 {
			ipLimit = 4
		}
		if n, err := s.emailVerify.IncrSendCount(ctx, "ip:"+clientIP, time.Hour); err == nil {
			if n > ipLimit {
				return ErrEmailCodeRateLimit
			}
		}
	}

	code, err := GenerateNumericCode(6)
	if err != nil {
		return err
	}
	if err := s.emailVerify.PutCode(ctx, emailAddr, code, s.emailCodeTTL); err != nil {
		return fmt.Errorf("auth.SendEmailVerification: put code: %w", err)
	}
	if err := s.emailVerify.MarkSentAt(ctx, emailAddr, now, s.emailCooldown); err != nil {
		s.log.Warn("auth: mark sent failed; cooldown may not apply", "err", err)
	}

	msg, err := s.emailRenderer.RenderVerificationCode(email.VerificationCodeData{
		Code:           code,
		ExpiresMinutes: int(s.emailCodeTTL / time.Minute),
		AppName:        s.emailAppName,
		SubjectTitle:   "邮箱验证码",
		Greeting:       "你正在注册一个新账号，",
		SupportEmail:   s.emailSupport,
		Year:           now.Year(),
	})
	if err != nil {
		return fmt.Errorf("auth.SendEmailVerification: render: %w", err)
	}
	msg.To = emailAddr
	if err := s.emailSender.Send(ctx, msg); err != nil {
		// 发送失败时**不删除**已 Put 的 code：让用户在 SMTP 抖动时仍可重试一次而不必"再申请"
		// （内存里其实有码，只是没收到邮件）；但 cooldown 已写入，下次 60s 内重试仍会被拒，
		// 攻击者无法借此频繁刷码空间。
		s.log.Error("auth: smtp send failed", "to", emailAddr, "err", err)
		return fmt.Errorf("auth.SendEmailVerification: send: %w", err)
	}
	s.log.Info("auth: email code sent", "to", emailAddr, "ttl", s.emailCodeTTL)
	return nil
}

// Register 注册新用户：检查 email 唯一 → IP 限制 → 评估密码强度 → HIBP 黑名单 → hash → Create。
//
// 密码门槛（按顺序）：
//  1. 长度/字符类强度（EvaluatePassword）：< minStrength → ErrWeakPassword
//  2. HIBP 黑名单（pwned > 0）→ ErrPwnedPassword
//
// HIBP 网络故障**fail-open**：仅 warn 日志，放行注册。理由：HIBP API 偶发 429
// 不应阻断用户注册流，否则等价 DoS。
//
// 当 minStrength = -1 时跳过强度+HIBP 两道（测试用）。
//
// "一 IP 一账号"策略（仅当 ipRegisterLimit = true）：
//   - clientIP 为空 ⇒ 跳过去重（视作"无法识别来源"，与 last_login_ip 默认值一致）
//   - clientIP 命中 IPRegisterAllowList ⇒ 跳过去重；register_ip 不入库
//   - 否则 ⇒ COUNT(register_ip = clientIP) > 0 时返回 ErrIPAlreadyRegistered；
//     入库时把 clientIP 持久化到 user.RegisterIP
//
// 反查时机故意放在 GetByEmail / 密码强度之后：避免攻击者用伪造的 email 探测
// "某 IP 是否注册过"——只有合法到能写库的请求才会触发去重失败。
//
// 邮箱验证码：
//   - 仅当 emailRequired=true（子系统已配置）时强制 code 非空；
//   - code 校验失败 ⇒ ErrEmailCodeInvalid（不区分错码 / 已过期 / 已消费）；
//   - 校验通过后立即 Delete 防重放。
//
// 邮箱域名规则（allow / block）：注册路径与 SendEmailVerification 共用同一份策略，
// 拒绝时返回 ErrEmailDomainNotAllowed；前端应在域名提示里展示允许的后缀列表。
func (s *Service) Register(ctx context.Context, emailAddr, password, clientIP, verificationCode string) (*User, error) {
	if emailAddr == "" || password == "" {
		return nil, ErrInvalidCredential
	}
	emailAddr = strings.TrimSpace(emailAddr)
	if !s.emailDomain.allows(emailAddr) {
		return nil, ErrEmailDomainNotAllowed
	}
	if s.minStrength >= 0 {
		if EvaluatePassword(password) < s.minStrength {
			return nil, ErrWeakPassword
		}
		if s.pwned != nil {
			pwned, err := s.pwned.IsPwned(ctx, password)
			switch {
			case err != nil:
				s.log.Warn("hibp lookup failed; fail-open", "err", err)
			case pwned:
				return nil, ErrPwnedPassword
			}
		}
	}
	if existing, _ := s.users.GetByEmail(ctx, emailAddr); existing != nil {
		return nil, ErrUserExists
	}

	// 邮箱验证码：仅当强制要求时校验。校验放在 email 唯一检查之后，
	// 避免攻击者通过"码错的提示"推断邮箱是否注册过——失败时永远是"码无效"，
	// 用户重复注册看到的是 ErrUserExists。
	if s.emailRequired {
		if s.emailVerify == nil {
			return nil, ErrEmailNotConfigured
		}
		if strings.TrimSpace(verificationCode) == "" {
			return nil, ErrEmailCodeRequired
		}
		stored, err := s.emailVerify.GetCode(ctx, emailAddr)
		if err != nil {
			return nil, ErrEmailCodeInvalid
		}
		if stored != strings.TrimSpace(verificationCode) {
			return nil, ErrEmailCodeInvalid
		}
		// 一次性消费：成功 → 立即删除，避免同码 N 次注册。
		// 后续步骤失败时不再恢复（重新申请一码即可，避免"窃码窗口"）。
		_ = s.emailVerify.DeleteCode(ctx, emailAddr)
	}

	// IP 去重：仅当 limit 启用、clientIP 非空、不命中白名单时才反查。
	persistIP := ""
	if s.ipRegisterLimit && clientIP != "" && !s.isRegisterIPAllowlisted(clientIP) {
		n, err := s.users.CountByRegisterIP(ctx, clientIP)
		if err != nil {
			// 反查失败保守拒绝（fail-closed）：DB 故障期间宁可拒注册也不放双开。
			s.log.Warn("auth: CountByRegisterIP failed; failing closed", "ip", clientIP, "err", err)
			return nil, err
		}
		if n > 0 {
			return nil, ErrIPAlreadyRegistered
		}
		persistIP = clientIP
	}

	hash, err := HashPassword(password, s.pwParams)
	if err != nil {
		return nil, err
	}

	// 启用激活流时，账号先落到 pending_email；登录路径会拒，必须先点击邮件链接激活。
	initialStatus := "active"
	if s.activateRequired {
		initialStatus = "pending_email"
	}

	u := &User{
		Email:        emailAddr,
		PasswordHash: hash,
		Status:       initialStatus,
		CreatedAt:    s.clock(),
		RegisterIP:   persistIP,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}

	// 注册成功后才发激活邮件：如果发送失败不要回滚账号——用户可通过 ResendActivation 重发，
	// 但要清晰记录失败原因，避免运维误以为"用户没注册成功"。
	if s.activateRequired {
		if err := s.sendActivationEmail(ctx, u); err != nil {
			s.log.Error("auth: activation email failed; user can resend", "user_id", u.ID, "email", u.Email, "err", err)
		}
	}
	return u, nil
}

// sendActivationEmail 生成激活 token、入库、渲染并发送邮件。
//
// 不在外部接口暴露：调用方一律走 Register / ResendActivation。
func (s *Service) sendActivationEmail(ctx context.Context, u *User) error {
	if s.activateTokens == nil || s.emailSender == nil || s.emailRenderer == nil || s.activateBaseURL == "" {
		return ErrEmailNotConfigured
	}
	token, err := NewActivationToken()
	if err != nil {
		return err
	}
	now := s.clock()
	tk := &ActivationToken{
		Token:     token,
		UserID:    u.ID,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.activateTTL),
	}
	if err := s.activateTokens.Put(ctx, tk); err != nil {
		return fmt.Errorf("auth.sendActivationEmail: put token: %w", err)
	}
	url := buildActivationURL(s.activateBaseURL, token)
	msg, err := s.emailRenderer.RenderActivationLink(email.ActivationLinkData{
		UserDisplay:  u.Email,
		ActivateURL:  url,
		ExpiresHours: int(s.activateTTL / time.Hour),
		AppName:      s.emailAppName,
		SupportEmail: s.emailSupport,
		Year:         now.Year(),
	})
	if err != nil {
		return fmt.Errorf("auth.sendActivationEmail: render: %w", err)
	}
	msg.To = u.Email
	if err := s.emailSender.Send(ctx, msg); err != nil {
		return fmt.Errorf("auth.sendActivationEmail: send: %w", err)
	}
	s.log.Info("auth: activation email sent", "to", u.Email, "ttl", s.activateTTL)
	return nil
}

// buildActivationURL 把 token 拼到 ActivationBaseURL 的 query string 上。
//
// 处理三种 base 形态：
//   1. "https://app/activate"            → "https://app/activate?token=xxx"
//   2. "https://app/activate?lang=zh"    → "https://app/activate?lang=zh&token=xxx"
//   3. "https://app/activate?token="     → "https://app/activate?token=xxx"（裸 token= 视为占位）
func buildActivationURL(base, token string) string {
	base = strings.TrimSpace(base)
	if strings.HasSuffix(base, "?token=") || strings.HasSuffix(base, "&token=") {
		return base + token
	}
	if strings.Contains(base, "?") {
		return base + "&token=" + token
	}
	return base + "?token=" + token
}

// ActivateAccount 用 token 激活 pending_email 账号。
//
// 流程：
//   1. tokens.GetByToken：返回已过期 / 已使用 / 不存在三种错误
//   2. users.GetByID：账号必须存在（被删过则视作无效 token）
//   3. 已 active：直接返回 ErrAccountAlreadyActive，仍消费 token 防滥用
//   4. 把 user.Status 改为 active 并 Update
//   5. tokens.MarkUsed：一次性消费防重放
//
// 返回更新后的 *User，前端可据此立即跳登录。
func (s *Service) ActivateAccount(ctx context.Context, token string) (*User, error) {
	if s.activateTokens == nil {
		return nil, ErrEmailNotConfigured
	}
	if strings.TrimSpace(token) == "" {
		return nil, ErrActivationTokenInvalid
	}
	tk, err := s.activateTokens.GetByToken(ctx, token)
	if err != nil {
		return nil, err // ErrActivationTokenInvalid / Expired / Used
	}
	u, err := s.users.GetByID(ctx, tk.UserID)
	if err != nil {
		return nil, ErrActivationTokenInvalid
	}
	if u.Status == "active" {
		// 防止"已激活的同一 token 再次使用"显得像没消费。仍 mark used 防滥用，但返回特定错误。
		_ = s.activateTokens.MarkUsed(ctx, token, s.clock())
		return u, ErrAccountAlreadyActive
	}
	if u.Status == "locked" || u.Status == "banned" || u.Status == "disabled" {
		return nil, ErrLocked
	}
	u.Status = "active"
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	if err := s.activateTokens.MarkUsed(ctx, token, s.clock()); err != nil {
		// 标记失败不致命：账号已 active，不影响登录；下次 GetByToken 仍会返回"未使用"，
		// 但因状态已 active，再次 ActivateAccount 走 ErrAccountAlreadyActive 分支。
		s.log.Warn("auth: mark activation token used failed", "err", err)
	}
	s.log.Info("auth: account activated", "user_id", u.ID, "email", u.Email)
	return u, nil
}

// ResendActivation 重发激活邮件。
//
// 安全策略：
//   - email 不存在 ⇒ 返回 nil（不暴露"该邮箱是否注册过"）
//   - 账号已 active ⇒ 也返回 nil（同上；前端 toast "如该邮箱有未激活账号，激活邮件已重发"）
//   - 频控走 EmailVerificationStore.IncrSendCount + cooldown，与发送验证码同一套配额
//
// 这样未登录用户调用此 API 不会被用作"邮箱探测器"。
func (s *Service) ResendActivation(ctx context.Context, emailAddr, clientIP string) error {
	emailAddr = strings.TrimSpace(emailAddr)
	if emailAddr == "" {
		return ErrInvalidCredential
	}
	if !s.emailDomain.allows(emailAddr) {
		return ErrEmailDomainNotAllowed
	}
	if s.emailSender == nil || s.activateTokens == nil || s.emailRenderer == nil || s.activateBaseURL == "" {
		return ErrEmailNotConfigured
	}

	// cooldown / rate limit（与发送验证码共用 store，避免被规避）
	if s.emailVerify != nil {
		now := s.clock()
		if last, err := s.emailVerify.GetLastSentAt(ctx, "activation:"+emailAddr); err == nil && !last.IsZero() {
			if now.Sub(last) < s.emailCooldown {
				return ErrEmailCodeCooldown
			}
		}
		if n, err := s.emailVerify.IncrSendCount(ctx, "activation-email:"+strings.ToLower(emailAddr), time.Hour); err == nil {
			if n > s.emailRLPerHour {
				return ErrEmailCodeRateLimit
			}
		}
		if clientIP != "" {
			ipLimit := s.emailRLPerHour * 4
			if ipLimit < 1 {
				ipLimit = 4
			}
			if n, err := s.emailVerify.IncrSendCount(ctx, "activation-ip:"+clientIP, time.Hour); err == nil {
				if n > ipLimit {
					return ErrEmailCodeRateLimit
				}
			}
		}
		_ = s.emailVerify.MarkSentAt(ctx, "activation:"+emailAddr, now, s.emailCooldown)
	}

	// 邮箱不存在 / 已激活 ⇒ 静默成功（防探测）
	u, err := s.users.GetByEmail(ctx, emailAddr)
	if err != nil || u == nil {
		s.log.Info("auth: resend activation skipped (email not registered)", "email", emailAddr)
		return nil
	}
	if u.Status == "active" {
		s.log.Info("auth: resend activation skipped (already active)", "email", emailAddr)
		return nil
	}
	if u.Status == "locked" || u.Status == "banned" || u.Status == "disabled" {
		// 不让被封号的账号刷激活邮件
		return ErrLocked
	}
	return s.sendActivationEmail(ctx, u)
}

// LoginResult 是 Login 的多态返回。
//
//   - Challenge != ""  ⇒  账号启用 2FA 且 totpCode 缺失/错误，前端应让用户输入 OTP
//                          再调 CompleteLoginTOTP(Challenge, code)。Session/User 此时为 nil。
//   - Session  != nil  ⇒  登录已完成（无 2FA 或 OTP 一并通过）。
//
// 同时返回二者是非法状态。
type LoginResult struct {
	Session   *Session
	User      *User
	Challenge string // challenge_id；仅当需要二步时非空
}

// Login 校验 email/password；若账号启用 TOTP 且 totpCode 缺失/错误，
// 则发起 challenge 让前端走第二步。
//
// 关于 totpCode 错误的语义：为了避免泄漏"密码对了但 2FA 错"，错误码也回退为
// challenge（前端表现成"请重新输入验证码"），不返回 ErrInvalidCredential。
//
// Session 固化策略：成功路径一律生成新 SID（不复用旧的）。
func (s *Service) Login(ctx context.Context, email, password, totpCode, ip, ua string) (*LoginResult, error) {
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil || u == nil {
		return nil, ErrInvalidCredential
	}
	if u.Status == "locked" || u.Status == "banned" || u.Status == "disabled" {
		return nil, ErrLocked
	}
	ok, err := VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
		return nil, ErrInvalidCredential
	}
	// 必须先验证密码才返回 pending_email，让"密码错"和"未激活"无法被攻击者借助 timing 区分
	// （防止用错误密码探测某邮箱是否已注册过）。
	if u.Status == "pending_email" {
		return nil, ErrAccountNotActivated
	}
	// 账号开启 TOTP：必须验证 OTP 才放行
	if u.TOTPEnabled {
		if s.totpChallenge == nil {
			// 服务端未配置 2FA 子系统但 DB 写着启用；保守拒登录，避免误放行
			return nil, ErrTOTPNotConfigured
		}
		// 一并提交了 OTP：直接验证，不挂 challenge
		if totpCode != "" {
			if err := s.verifyAndConsumeTOTP(ctx, u, totpCode); err != nil {
				return nil, err
			}
			sess, err := s.issueSession(ctx, u, ip, ua)
			if err != nil {
				return nil, err
			}
			return &LoginResult{Session: sess, User: u}, nil
		}
		// 未带 OTP：发 challenge
		id, err := NewChallengeID()
		if err != nil {
			return nil, err
		}
		ch := &TOTPChallenge{
			UserID:    u.ID,
			IP:        ip,
			UserAgent: ua,
			IssuedAt:  s.clock(),
		}
		if err := s.totpChallenge.Put(ctx, id, ch, 5*time.Minute); err != nil {
			return nil, err
		}
		return &LoginResult{Challenge: id}, nil
	}
	sess, err := s.issueSession(ctx, u, ip, ua)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Session: sess, User: u}, nil
}

// CompleteLoginTOTP 完成登录第二步：用 challenge_id 取出 user，验证 OTP，签发 Session。
func (s *Service) CompleteLoginTOTP(ctx context.Context, challengeID, code, ip, ua string) (*Session, *User, error) {
	if s.totpChallenge == nil {
		return nil, nil, ErrTOTPNotConfigured
	}
	if challengeID == "" || code == "" {
		return nil, nil, ErrInvalidCredential
	}
	ch, err := s.totpChallenge.Get(ctx, challengeID)
	if err != nil {
		return nil, nil, err
	}
	u, err := s.users.GetByID(ctx, ch.UserID)
	if err != nil || u == nil {
		return nil, nil, ErrUserNotFound
	}
	if u.Status == "locked" || u.Status == "banned" {
		return nil, nil, ErrLocked
	}
	if !u.TOTPEnabled {
		// challenge 创建后用户在另一个会话里关掉了 2FA：当作 challenge 已失效
		_ = s.totpChallenge.Delete(ctx, challengeID)
		return nil, nil, ErrTOTPChallengeNotFound
	}
	if err := s.verifyAndConsumeTOTP(ctx, u, code); err != nil {
		return nil, nil, err
	}
	// challenge 一次性消费
	_ = s.totpChallenge.Delete(ctx, challengeID)

	sess, err := s.issueSession(ctx, u, ip, ua)
	if err != nil {
		return nil, nil, err
	}
	return sess, u, nil
}

// issueSession 生成新 SID 并落库；登录 / 二步登录 / 重新登录共用。
func (s *Service) issueSession(ctx context.Context, u *User, ip, ua string) (*Session, error) {
	sid, err := NewSID()
	if err != nil {
		return nil, err
	}
	now := s.clock()
	sess := &Session{
		SID:       sid,
		UserID:    u.ID,
		IP:        ip,
		UserAgent: ua,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.sessTTL),
	}
	if err := s.sessions.Save(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// IssueSessionFor 给定用户直接签发新 session; 供 OAuth 第三方登录后调用.
//
// 与 Login 不同: 不验密码 / 不查 TOTP. 调用方必须保证 u 已经通过外部强认证 (e.g. linux.do callback 已校验 sub).
//
// **重要**: caller 负责检查 u.Status (locked/disabled 不应签发); 本方法不做策略, 只生成 SID 落库.
// 这是有意为之 — 让 OAuth service 自己决定"邮箱验证 / 激活" 的 OAuth 例外策略
// (e.g. linux.do 已经验过邮箱, 跳过本地激活).
func (s *Service) IssueSessionFor(ctx context.Context, u *User, ip, ua string) (*Session, error) {
	return s.issueSession(ctx, u, ip, ua)
}

// FindUserByEmail 是 GetByEmail 的语义化封装, 供 OAuth callback 检查邮箱占用情况.
// 找不到返回 (nil, nil) — 不视为错误 (用于"邮箱是否已注册"探查).
func (s *Service) FindUserByEmail(ctx context.Context, email string) (*User, error) {
	if email == "" {
		return nil, nil
	}
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil || u == nil {
		return nil, nil
	}
	return u, nil
}

// CreateOAuthLinkedUser 用 OAuth profile 创建一个新本地账号.
//
// 行为:
//   - status = "active" (跳过本地邮箱验证: linux.do 等 provider 已经验过邮箱);
//   - password_hash = 一段不可登录的占位 (随机 32 字节 hex; 用户必须走第三方登录或后续设密码);
//   - email 可空 (provider 不返回邮箱时, 用户只能用 OAuth 登录, 不能用密码/邮箱重置).
//
// 调用方 (oauth.Service) 在调用前必须已确认 email 不会跟现有账号冲突 (找到的话走绑定流而不是建账号).
func (s *Service) CreateOAuthLinkedUser(ctx context.Context, email string) (*User, error) {
	// 32 字节随机, 用 argon2id 生成不可逆的占位 hash.
	// 用户后续可通过密码重置流程 (邮箱激活后) 设密码.
	placeholderToken, err := newOAuthPlaceholderPassword()
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(placeholderToken, s.pwParams)
	if err != nil {
		return nil, err
	}
	u := &User{
		Email:        email,
		PasswordHash: hash,
		Status:       "active",
		Role:         RoleUser,
		PlanID:       "free",
		CreatedAt:    s.clock(),
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// newOAuthPlaceholderPassword 生成一段足够强的随机字节, 当作 OAuth-only 账号的占位密码原文.
// 调用方不会持有原文, 只入库 hash.
func newOAuthPlaceholderPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := cryptoRand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// verifyAndConsumeTOTP 校验 6 位码 + 通过 replay blocker 防重放；任一失败返回 ErrTOTPInvalid。
//
// 不暴露具体失败原因（"码错"/"已用过"/"漂移过大"统一同一个错误，避免侧信道暴露）。
func (s *Service) verifyAndConsumeTOTP(ctx context.Context, u *User, code string) error {
	if u == nil || u.TOTPSecret == "" {
		return ErrTOTPInvalid
	}
	if !TOTPVerify(u.TOTPSecret, code) {
		return ErrTOTPInvalid
	}
	if s.totpReplay != nil {
		ok, err := s.totpReplay.CheckAndMark(ctx, u.ID, code)
		if err != nil {
			// Redis 故障 → 拒绝（fail-closed）；避免无限重放
			s.log.Warn("auth.TOTP replay blocker error; rejecting", "user", u.ID, "err", err)
			return ErrTOTPInvalid
		}
		if !ok {
			return ErrTOTPInvalid
		}
	}
	return nil
}

// Logout 删除 Session。
func (s *Service) Logout(ctx context.Context, sid string) error {
	return s.sessions.Delete(ctx, sid)
}

// VerifySession 校验 SID 有效性 & 反查 user。
func (s *Service) VerifySession(ctx context.Context, sid string) (*User, *Session, error) {
	sess, err := s.sessions.Get(ctx, sid)
	if err != nil {
		return nil, nil, err
	}
	if !sess.ExpiresAt.IsZero() && sess.ExpiresAt.Before(s.clock()) {
		_ = s.sessions.Delete(ctx, sid)
		return nil, nil, ErrSessionNotFound
	}
	u, err := s.users.GetByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	return u, sess, nil
}

// ChangePassword 修改自己密码（旧密码校验 + 新密码强度）。
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if ok, _ := VerifyPassword(oldPassword, u.PasswordHash); !ok {
		return ErrInvalidCredential
	}
	if EvaluatePassword(newPassword) < s.minStrength {
		return ErrWeakPassword
	}
	if s.pwned != nil {
		if pwned, hibpErr := s.pwned.IsPwned(ctx, newPassword); hibpErr == nil && pwned {
			return ErrPwnedPassword
		}
	}
	hash, err := HashPassword(newPassword, s.pwParams)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	return s.users.Update(ctx, u)
}

// PromoteToAdmin 把用户角色升为 admin（仅 superadmin 应该有权限调用；
// 由调用方在外层 RBAC middleware 强制）。
func (s *Service) PromoteToAdmin(ctx context.Context, userID string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.Role = RoleAdmin
	return s.users.Update(ctx, u)
}

// SetRole 显式设置用户角色（user / admin / superadmin）。
func (s *Service) SetRole(ctx context.Context, userID, role string) error {
	if role != RoleUser && role != RoleAdmin && role != RoleSuperAdmin {
		return errors.New("auth: invalid role")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.Role = role
	return s.users.Update(ctx, u)
}

// SetUserPlan 把用户的"当前套餐"改成 planID。新创建的 API Key 会继承此值。
//
// 不会自动升级用户已有的 API Key（管理员另行 SetAPIKeyPlan 或 admin handler 批量切）。
func (s *Service) SetUserPlan(ctx context.Context, userID, planID string) error {
	if planID == "" {
		return errors.New("auth: empty planID")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.PlanID = planID
	return s.users.Update(ctx, u)
}

// SetStatus 设置账号状态（active / locked / disabled）。
func (s *Service) SetStatus(ctx context.Context, userID, status string) error {
	switch status {
	case "active", "locked", "disabled", "pending_email":
	default:
		return errors.New("auth: invalid status")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.Status = status
	return s.users.Update(ctx, u)
}

// AdminResetPassword 由超级管理员强制重置用户密码。
//
// 不做强度/HIBP 检查（管理员可临时设短密码后让用户登录改密）；返回 hash 失败的错误。
func (s *Service) AdminResetPassword(ctx context.Context, userID, newPlain string) error {
	if newPlain == "" {
		return errors.New("auth: empty new password")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	hash, err := HashPassword(newPlain, s.pwParams)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	return s.users.Update(ctx, u)
}

// GetUserByID 暴露 UserRepo.GetByID（admin 后台用）。
func (s *Service) GetUserByID(ctx context.Context, userID string) (*User, error) {
	return s.users.GetByID(ctx, userID)
}

// ListUsers 列举用户（仅 admin 后台用）。
//
// 内部依赖 UserRepo 实现 UserLister 接口；当前 PG / Mem 仓库均已实现。
// 不实现时返回 ErrNotSupported。
func (s *Service) ListUsers(ctx context.Context, filter ListUserFilter) ([]*User, int, error) {
	lister, ok := s.users.(UserLister)
	if !ok {
		return nil, 0, errors.New("auth: list users not supported by this repo")
	}
	return lister.ListUsers(ctx, filter)
}

// ---- API Key CRUD（要求 NewService 时传入 APIKeyRepo） ----

// ErrAPIKeyNotEnabled 表示构造 Service 时未提供 APIKeyRepo。
var ErrAPIKeyNotEnabled = errors.New("auth: API Key store not configured")

// CreateAPIKeyInput 是创建 API Key 的参数。
type CreateAPIKeyInput struct {
	Name           string
	Description    string
	Scopes         []string
	IPAllow        []string
	RateLimitRPM   int
	RateLimitDaily int64
	ExpiresAt      time.Time
}

// CreateAPIKey 为用户创建一把 API Key。明文 plain 仅本次返回。
//
// expiresAt 零值 = 永不过期；scopes 推荐 [resource]:[action] 格式（如 "music:read"）。
//
// 新 Key 的套餐继承用户当前 PlanID（u.EffectivePlanID()）。这样用户升到 enterprise
// 后再建 Key 也会是 enterprise，而不是回到 free。
func (s *Service) CreateAPIKey(ctx context.Context, userID string, input CreateAPIKeyInput) (plain string, rec *APIKeyRecord, err error) {
	if s.apiKeys == nil {
		return "", nil, ErrAPIKeyNotEnabled
	}
	if userID == "" {
		return "", nil, errors.New("auth.CreateAPIKey: empty user_id")
	}
	scopes, err := normalizeScopes(input.Scopes)
	if err != nil {
		return "", nil, err
	}
	ipAllow, err := normalizeIPAllow(input.IPAllow)
	if err != nil {
		return "", nil, err
	}
	k, err := NewAPIKey(s.pwParams)
	if err != nil {
		return "", nil, err
	}
	planID := "free"
	if u, uerr := s.users.GetByID(ctx, userID); uerr == nil {
		planID = u.EffectivePlanID()
	}
	r := &APIKeyRecord{
		ID:             newAPIKeyID(),
		UserID:         userID,
		Prefix:         k.Prefix,
		Hash:           k.Hash,
		Name:           input.Name,
		Description:    input.Description,
		PlanID:         planID,
		Scopes:         scopes,
		IPAllow:        ipAllow,
		RateLimitRPM:   input.RateLimitRPM,
		RateLimitDaily: input.RateLimitDaily,
		Enabled:        true,
		CreatedAt:      s.clock(),
		ExpiresAt:      input.ExpiresAt,
	}
	if err := s.apiKeys.Create(ctx, r); err != nil {
		return "", nil, err
	}
	return k.Plain, r, nil
}

// normalizeScopes 服务端校验 + 去重 + 排序，保证落库的 scope 列表干净。
//
// 规则：每个 scope 必须 IsValidGrantedScope 通过；空字符串 / 重复项移除；
// 任一非法直接返回 ErrInvalidScope（前端已在 token 输入处校验过，这里是安全网）。
func normalizeScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = trimSpace(s)
		if s == "" {
			continue
		}
		if !IsValidGrantedScope(s) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidScope, s)
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// normalizeIPAllow 服务端校验 + 去重；每条必须是 IP 或 CIDR。
func normalizeIPAllow(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := trimSpace(raw)
		if s == "" {
			continue
		}
		if !isValidIPOrCIDR(s) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidIPAllow, s)
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ListAPIKeys 列出用户的全部 API Key（不含明文）。
func (s *Service) ListAPIKeys(ctx context.Context, userID string) ([]*APIKeyRecord, error) {
	if s.apiKeys == nil {
		return nil, ErrAPIKeyNotEnabled
	}
	return s.apiKeys.ListByUser(ctx, userID)
}

// RevokeAPIKey 把指定 ID 的 API Key 标记为已吊销（仅 owner 可调用）。
func (s *Service) RevokeAPIKey(ctx context.Context, userID, id string) error {
	if s.apiKeys == nil {
		return ErrAPIKeyNotEnabled
	}
	return s.apiKeys.Revoke(ctx, id, userID, s.clock())
}

// UpdateAPIKeyInput 是更新 API Key 的参数（部分更新）。
type UpdateAPIKeyInput struct {
	Name           *string
	Description    *string
	Scopes         []string
	IPAllow        []string
	RateLimitRPM   *int
	RateLimitDaily *int64
	ExpiresAt      *time.Time
}

// UpdateAPIKey 更新指定 API Key 的可变配置。
func (s *Service) UpdateAPIKey(ctx context.Context, userID, id string, input UpdateAPIKeyInput) (*APIKeyRecord, error) {
	if s.apiKeys == nil {
		return nil, ErrAPIKeyNotEnabled
	}
	rec, err := s.apiKeys.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	if !rec.RevokedAt.IsZero() {
		return nil, ErrAPIKeyNotFound
	}
	if input.Name != nil {
		rec.Name = *input.Name
	}
	if input.Description != nil {
		rec.Description = *input.Description
	}
	if input.Scopes != nil {
		ns, nerr := normalizeScopes(input.Scopes)
		if nerr != nil {
			return nil, nerr
		}
		rec.Scopes = ns
	}
	if input.IPAllow != nil {
		ni, nerr := normalizeIPAllow(input.IPAllow)
		if nerr != nil {
			return nil, nerr
		}
		rec.IPAllow = ni
	}
	if input.RateLimitRPM != nil {
		rec.RateLimitRPM = *input.RateLimitRPM
	}
	if input.RateLimitDaily != nil {
		rec.RateLimitDaily = *input.RateLimitDaily
	}
	if input.ExpiresAt != nil {
		rec.ExpiresAt = *input.ExpiresAt
	}
	if err := s.apiKeys.Update(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// SetAPIKeyEnabled 切换 API Key 的启用/禁用状态。
func (s *Service) SetAPIKeyEnabled(ctx context.Context, userID, id string, enabled bool) error {
	if s.apiKeys == nil {
		return ErrAPIKeyNotEnabled
	}
	return s.apiKeys.SetEnabled(ctx, id, userID, enabled)
}

// GetAPIKey 获取单个 API Key 详情（仅 owner）。
func (s *Service) GetAPIKey(ctx context.Context, userID, id string) (*APIKeyRecord, error) {
	if s.apiKeys == nil {
		return nil, ErrAPIKeyNotEnabled
	}
	rec, err := s.apiKeys.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	return rec, nil
}

// SetAPIKeyPlan 设置 API Key 的套餐（升级/降级）。
func (s *Service) SetAPIKeyPlan(ctx context.Context, userID, id, planID string) error {
	if s.apiKeys == nil {
		return ErrAPIKeyNotEnabled
	}
	rec, err := s.apiKeys.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if rec.UserID != userID {
		return ErrAPIKeyNotFound
	}
	rec.PlanID = planID
	return s.apiKeys.Update(ctx, rec)
}

// ErrAPIKeyDisabled 表示 API Key 已被禁用。
var ErrAPIKeyDisabled = errors.New("auth: api key disabled")

// VerifyAPIKey 用明文 key 验签：按 prefix 查 → argon2id 比对 → 检查活性 → IP 白名单 → Touch。
//
// 参数 clientIP 为请求来源（c.ClientIP 已经穿透代理；middleware 调用方负责传入）。
// 空字符串视为"未知 IP"，仅当 key 配置了 IPAllow 时才会被拒（白名单存在 ⇒ 必须能匹配）。
//
// 返回归属用户记录；失败返回：
//   - ErrAPIKeyMalformed   prefix/格式错
//   - ErrAPIKeyInvalid     全部候选都不匹配
//   - ErrAPIKeyIPNotAllowed 验签通过但来源 IP 不在白名单
func (s *Service) VerifyAPIKey(ctx context.Context, plain, clientIP string) (*User, *APIKeyRecord, error) {
	if s.apiKeys == nil {
		return nil, nil, ErrAPIKeyNotEnabled
	}
	if len(plain) < 11 || plain[:3] != "mk_" {
		return nil, nil, ErrAPIKeyMalformed
	}
	prefix := plain[:8]
	candidates, err := s.apiKeys.GetByPrefix(ctx, prefix)
	if err != nil {
		return nil, nil, err
	}
	now := s.clock()
	for _, c := range candidates {
		if !c.IsActive(now) {
			continue
		}
		ok, _ := VerifyPassword(plain, c.Hash)
		if !ok {
			continue
		}
		// 验签通过后再做 IP 白名单：让"无效 key"和"IP 不在白名单"返回不同的错，
		// 攻击者拿不到的 key 无法触达 IP 路径，所以信息泄漏可控。
		if !c.IsIPAllowed(clientIP) {
			return nil, nil, ErrAPIKeyIPNotAllowed
		}
		u, err := s.users.GetByID(ctx, c.UserID)
		if err != nil {
			return nil, nil, err
		}
		_ = s.apiKeys.Touch(ctx, c.ID, now)
		c.LastUsedAt = now
		c.TotalRequests++
		return u, c, nil
	}
	return nil, nil, ErrAPIKeyInvalid
}

// MemUserRepo 是 in-memory 仓库（测试 + e2e 启动期）。
type MemUserRepo struct {
	mu      sync.RWMutex
	byEmail map[string]*User
	byID    map[string]*User
	nextID  int64
}

// NewMemUserRepo 构造空仓库。
func NewMemUserRepo() *MemUserRepo {
	return &MemUserRepo{
		byEmail: map[string]*User{},
		byID:    map[string]*User{},
	}
}

// GetByEmail 实现 UserRepo.GetByEmail；不存在返回 (nil, ErrUserNotFound)。
func (r *MemUserRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byEmail[email]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

// GetByID 实现 UserRepo.GetByID。
func (r *MemUserRepo) GetByID(_ context.Context, id string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byID[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

// Create 实现 UserRepo.Create；自动赋递增 ID。
func (r *MemUserRepo) Create(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byEmail[u.Email]; dup {
		return ErrUserExists
	}
	r.nextID++
	if u.ID == "" {
		u.ID = formatID(r.nextID)
	}
	cp := *u
	r.byEmail[u.Email] = &cp
	r.byID[u.ID] = &cp
	return nil
}

// Update 实现 UserRepo.Update。
func (r *MemUserRepo) Update(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[u.ID]; !ok {
		return ErrUserNotFound
	}
	cp := *u
	r.byID[u.ID] = &cp
	r.byEmail[u.Email] = &cp
	return nil
}

// CountByRegisterIP 实现 UserRepo.CountByRegisterIP（mem 仓库线性扫描）。
func (r *MemUserRepo) CountByRegisterIP(_ context.Context, ip string) (int, error) {
	if ip == "" {
		return 0, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, u := range r.byID {
		if u.RegisterIP == ip {
			n++
		}
	}
	return n, nil
}

// ListUsers 实现 UserLister 接口（管理员后台用）。
func (r *MemUserRepo) ListUsers(_ context.Context, f ListUserFilter) ([]*User, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	matches := make([]*User, 0)
	for _, u := range r.byID {
		if f.EmailLike != "" && !containsFold(u.Email, f.EmailLike) {
			continue
		}
		if f.Role != "" && u.EffectiveRole() != f.Role {
			continue
		}
		if f.Status != "" && u.Status != f.Status {
			continue
		}
		matches = append(matches, u)
	}
	total := len(matches)
	// 按 CreatedAt 倒序
	sortUsersByCreatedDesc(matches)
	if offset >= len(matches) {
		return []*User{}, total, nil
	}
	end := offset + limit
	if end > len(matches) {
		end = len(matches)
	}
	out := make([]*User, 0, end-offset)
	for _, u := range matches[offset:end] {
		cp := *u
		out = append(out, &cp)
	}
	return out, total, nil
}

func containsFold(haystack, needle string) bool {
	hs := []rune(haystack)
	ne := []rune(needle)
	if len(ne) == 0 || len(hs) < len(ne) {
		return len(ne) == 0
	}
	for i := 0; i+len(ne) <= len(hs); i++ {
		match := true
		for j := 0; j < len(ne); j++ {
			a := hs[i+j]
			b := ne[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func sortUsersByCreatedDesc(us []*User) {
	for i := 0; i < len(us)-1; i++ {
		for j := i + 1; j < len(us); j++ {
			if us[j].CreatedAt.After(us[i].CreatedAt) {
				us[i], us[j] = us[j], us[i]
			}
		}
	}
}

func formatID(n int64) string {
	// 简单 base36 编码避免 strconv 导入；只用于 mem repo 测试。
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%36]
		n /= 36
	}
	return "u_" + string(b[i:])
}
