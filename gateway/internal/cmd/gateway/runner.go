// Package gateway 是 Edge Gateway (REST + gRPC) 的 wiring runner。
//
// **配置优先级**：CLI flag > MGW_GATEWAY_<KEY> > MGW_<KEY> > legacy env > default
package gateway

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	"github.com/MiChongs/QQMusicApi/gateway/internal/auth/oauth"
	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	pkggateway "github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	"github.com/MiChongs/QQMusicApi/gateway/migrations"
	"github.com/MiChongs/QQMusicApi/gateway/internal/store/crypto"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing/payment"
	authv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/auth/v1"
	billingv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/billing/v1"
	musicv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/music/v1"
	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/music"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/netease"
	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusicprovider"
	"github.com/MiChongs/QQMusicApi/gateway/internal/store"
)

// Version 由 ldflags 注入。
var Version = "v0.3.0-dev"

// Run 启动 Edge Gateway；阻塞到 ctx 取消。
func Run(ctx context.Context, args []string) error {
	l := cmdpkg.NewLoader("gateway")
	l.String("http", ":8080", "HTTP listen address")
	l.String("grpc", ":9000", "gRPC listen address")
	l.String("redis", "127.0.0.1:6379", "Redis address (used for sessions)")
	l.String("redis-password", "", "Redis password")
	l.Duration("session-ttl", 7*24*time.Hour, "session TTL")
	l.String("admin-token", "", "comma-separated admin tokens (empty = /v1/admin/* disabled)")
	l.String("bootstrap-email", "admin@example.com", "email for first-run superadmin auto-bootstrap")
	l.Bool("bootstrap-disable", false, "disable first-run superadmin auto-bootstrap")
	l.String("pg", "", "PostgreSQL DSN for auth schema (preferred); empty falls back to -user-store")
	l.String("user-store", "data/users.json", "JSON-backed user store fallback when -pg empty; empty = in-memory only")
	l.String("timezone", "", "global timezone (IANA name like Asia/Shanghai or UTC); empty = follow system TZ env / /etc/localtime")
	l.Bool("register-ip-limit", false, "enforce one-account-per-IP at registration (default off; allowlist via -register-ip-allowlist)")
	l.String("register-ip-allowlist", "", "comma-separated IP/CIDR allowlist exempt from one-account-per-IP (e.g. \"127.0.0.1,10.0.0.0/8\")")
	// SMTP / 邮箱验证（"获取验证码 + 注册"）。SMTP 配置缺失时使用 LogSender，把验证码打到 stdout。
	l.String("smtp-host", "", "SMTP server host; empty = log-only sender (codes printed to stdout, no real email)")
	l.Int("smtp-port", 587, "SMTP server port (587 STARTTLS / 465 implicit TLS / 25 plain)")
	l.String("smtp-user", "", "SMTP authentication username")
	l.String("smtp-password", "", "SMTP authentication password")
	l.String("smtp-from", "", "From address for outgoing emails (required when -smtp-host non-empty)")
	l.String("smtp-from-name", "", "Optional display name shown before the From address")
	l.String("smtp-tls", "auto", "SMTP TLS mode: auto / starttls / ssl / none")
	l.Bool("smtp-tls-insecure", false, "Skip TLS certificate verification (dev only; never in prod)")
	l.Duration("smtp-timeout", 15*time.Second, "SMTP dial / send timeout")
	l.Bool("email-verify-required", false, "require email verification code at registration (auto-disabled if SMTP not configured)")
	l.String("email-allowed-domains", "", "comma-separated domain whitelist (\"qq.com,*.edu.cn\"); empty = no restriction")
	l.String("email-blocked-domains", "", "comma-separated domain blocklist (e.g. disposable mail providers); always wins over allow-list")
	l.Duration("email-code-ttl", 10*time.Minute, "verification code valid lifetime")
	l.Duration("email-code-cooldown", 60*time.Second, "minimum interval between two send-code calls for the same email")
	l.Int("email-code-hourly-limit", 5, "max send-code calls per email per hour (per-IP limit auto = 4×)")
	l.String("email-app-name", "", "branding shown in verification emails; empty = TOTP issuer (\"Karpov\")")
	l.String("email-support", "", "optional support email shown in the email footer")
	l.Bool("activation-required", false, "require email activation link click after registration (account starts pending_email)")
	l.String("activation-base-url", "", "frontend base URL for the activation page (e.g. https://app.example.com/activate); required when -activation-required true")
	l.Duration("activation-ttl", 24*time.Hour, "activation link valid lifetime")
	// ---- OAuth (Linux.do) ----
	l.Bool("oauth-linuxdo-enabled", false, "enable Linux.do OAuth2 SSO provider")
	l.String("oauth-linuxdo-client-id", "", "Linux.do connect.linux.do client_id")
	l.String("oauth-linuxdo-client-secret", "", "Linux.do connect.linux.do client_secret")
	l.String("oauth-linuxdo-redirect-url", "", "absolute callback URL (e.g. https://gateway.karpov.cn/v1/auth/oauth/linuxdo/callback); empty = derive from -oauth-public-base + path")
	l.Int("oauth-linuxdo-min-trust-level", 0, "minimum Linux.do trust_level required to log in (0 = no restriction)")
	l.Bool("oauth-linuxdo-require-active", true, "reject Linux.do accounts where active=false (silenced/unverified)")
	l.String("oauth-public-base", "", "public-facing gateway base URL (https://gateway.karpov.cn); used to build OAuth callback / front-end redirect")
	l.String("oauth-frontend-base", "", "front-end base URL (https://gateway.karpov.cn); used as success/error redirect target; empty = derive from -oauth-public-base")
	l.Bool("version", false, "print version and exit")
	l.LegacyEnv("redis-password", "REDIS_PASSWORD")
	l.LegacyEnv("admin-token", "ADMIN_TOKEN")
	l.LegacyEnv("user-store", "AUTH_USER_STORE")
	l.LegacyEnv("pg", "DATABASE_URL", "AUTH_PG", "POSTGRES_DSN")
	l.LegacyEnv("timezone", "TZ", "TIMEZONE")
	l.LegacyEnv("register-ip-limit", "REGISTER_IP_LIMIT")
	l.LegacyEnv("register-ip-allowlist", "REGISTER_IP_ALLOWLIST")
	l.LegacyEnv("smtp-host", "SMTP_HOST")
	l.LegacyEnv("smtp-port", "SMTP_PORT")
	l.LegacyEnv("smtp-user", "SMTP_USER", "SMTP_USERNAME")
	l.LegacyEnv("smtp-password", "SMTP_PASSWORD", "SMTP_PASS")
	l.LegacyEnv("smtp-from", "SMTP_FROM", "MAIL_FROM")
	l.LegacyEnv("smtp-from-name", "SMTP_FROM_NAME", "MAIL_FROM_NAME")
	l.LegacyEnv("smtp-tls", "SMTP_TLS")
	l.LegacyEnv("email-verify-required", "EMAIL_VERIFY_REQUIRED")
	l.LegacyEnv("email-allowed-domains", "EMAIL_ALLOWED_DOMAINS")
	l.LegacyEnv("email-blocked-domains", "EMAIL_BLOCKED_DOMAINS")
	l.LegacyEnv("email-app-name", "EMAIL_APP_NAME")
	l.LegacyEnv("email-support", "EMAIL_SUPPORT")
	l.LegacyEnv("activation-required", "ACTIVATION_REQUIRED")
	l.LegacyEnv("activation-base-url", "ACTIVATION_BASE_URL")
	l.LegacyEnv("activation-ttl", "ACTIVATION_TTL")
	l.LegacyEnv("oauth-linuxdo-enabled", "OAUTH_LINUXDO_ENABLED")
	l.LegacyEnv("oauth-linuxdo-client-id", "OAUTH_LINUXDO_CLIENT_ID")
	l.LegacyEnv("oauth-linuxdo-client-secret", "OAUTH_LINUXDO_CLIENT_SECRET")
	l.LegacyEnv("oauth-linuxdo-redirect-url", "OAUTH_LINUXDO_REDIRECT_URL")
	l.LegacyEnv("oauth-linuxdo-min-trust-level", "OAUTH_LINUXDO_MIN_TRUST_LEVEL")
	l.LegacyEnv("oauth-linuxdo-require-active", "OAUTH_LINUXDO_REQUIRE_ACTIVE")
	l.LegacyEnv("oauth-public-base", "OAUTH_PUBLIC_BASE", "PUBLIC_BASE_URL")
	l.LegacyEnv("oauth-frontend-base", "OAUTH_FRONTEND_BASE", "NEXT_PUBLIC_APP_URL")
	if stop, err := l.Parse(args); err != nil {
		return err
	} else if stop {
		return nil
	}
	if l.GetBool("version") {
		fmt.Println(Version)
		return nil
	}

	httpAddr := l.GetString("http")
	grpcAddr := l.GetString("grpc")
	redisAddr := cmdpkg.ComposeRedisAddr(l.GetString("redis"))
	redisPassword := l.GetString("redis-password")
	if redisPassword == "" {
		redisPassword = os.Getenv("REDIS_PASSWORD")
	}

	// ---- Timezone：进程级时区。空 = 跟随系统；非空 = 覆盖 time.Local + PG session timezone。
	// 在 logger 前 apply：让 logger 的 timestamp 也用配置时区显示。
	tzName := l.GetString("timezone")
	tzLoc, err := cmdpkg.ResolveTimeZone(tzName)
	if err != nil {
		return fmt.Errorf("edge gateway: %w", err)
	}
	cmdpkg.ApplyProcessTimeZone(tzLoc)
	resolvedTZ := cmdpkg.TimeZoneName(tzLoc)

	logger := observability.NewLogger("edge-gateway", slog.LevelInfo)
	logger.Info("edge gateway starting",
		"version", Version, "http", httpAddr, "grpc", grpcAddr,
		"timezone", resolvedTZ, "tz_source", tzSourceLabel(tzName))

	// ---- Redis（Session 存储）----
	rdb, err := store.NewRedisClient(ctx, store.RedisConfig{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	if err != nil {
		logger.Error("redis connect failed", "addr", redisAddr,
			"has_password", redisPassword != "", "err", err)
		return fmt.Errorf("edge gateway: %w", err)
	}
	defer func() { _ = rdb.Close() }()
	logger.Info("redis connected", "addr", redisAddr, "auth", redisPassword != "")

	// ---- Auth Service：用户仓库优先级 ----
	// 1) -pg / MGW_GATEWAY_PG / DATABASE_URL → PG（production-grade）；
	// 2) POSTGRES_USER + POSTGRES_PASSWORD（自动拼装 DSN，与 deploy/compose/.env 共享）；
	// 3) -user-store / MGW_GATEWAY_USER_STORE → JSON 文件兜底（开发期跨重启）；
	// 4) 都为空 → 进程内内存（仅用于一次性脚本，重启即丢，会强警告）。
	//
	// PG 路径会自动跑 auth schema 迁移（embed 的 SQL），并自动愈合 dirty 状态。
	pgDSN := cmdpkg.ComposePGDSN(l.GetString("pg"))
	if pgDSN != "" && tzName != "" {
		// 把 timezone 放到 DSN 上：pgx 会作为 RuntimeParams 在 StartupMessage 阶段下发，
		// session 级 SET TIME ZONE 等价。失败不阻塞——回到没显式设置的状态。
		if withTZ, terr := store.AppendTimeZone(pgDSN, resolvedTZ); terr == nil {
			pgDSN = withTZ
		} else {
			logger.Warn("attach pg timezone failed; pg session will use server default", "err", terr)
		}
	}
	userStorePath := l.GetString("user-store")
	ar, err := buildAuthRepos(ctx, logger, pgDSN, userStorePath)
	if err != nil {
		return fmt.Errorf("edge gateway: auth: %w", err)
	}
	logger.Info("auth ready", "backend", ar.label)

	// 注册 IP 限制（"一 IP 一账号"）：默认关闭。
	// allowlist 是 CSV：单 IP 或 CIDR；空字符串视作"全部 IP 都参与去重"。
	ipRegisterAllowList := splitCSV(l.GetString("register-ip-allowlist"))

	// ---- SMTP / 邮箱验证子系统装配 ----
	emailSender, emailSenderLabel := buildEmailSender(logger, l)
	emailVerifyStore := auth.NewRedisEmailVerificationStore(rdb, "")
	allowedDomains := splitCSV(l.GetString("email-allowed-domains"))
	blockedDomains := splitCSV(l.GetString("email-blocked-domains"))

	sessions := auth.NewRedisSessionStore(rdb, "")
	authSvc := auth.NewService(ar.users, sessions, auth.Options{
		PasswordParams:            auth.DefaultPasswordParams(),
		SessionTTL:                l.GetDuration("session-ttl"),
		APIKeyRepo:                ar.apiKeys,
		Logger:                    logger,
		TOTPIssuer:                "Karpov",
		TOTPPendingStore:          auth.NewRedisPendingTOTPStore(rdb, ""),
		TOTPChallengeStore:        auth.NewRedisTOTPChallengeStore(rdb, ""),
		TOTPReplayBlocker:         auth.NewTOTPReplayBlocker(rdb, ""),
		IPRegisterLimit:           l.GetBool("register-ip-limit"),
		IPRegisterAllowList:       ipRegisterAllowList,
		EmailSender:               emailSender,
		EmailVerification:         emailVerifyStore,
		EmailVerificationRequired: l.GetBool("email-verify-required"),
		EmailAllowedDomains:       allowedDomains,
		EmailBlockedDomains:       blockedDomains,
		EmailCodeTTL:              l.GetDuration("email-code-ttl"),
		EmailCodeCooldown:         l.GetDuration("email-code-cooldown"),
		EmailCodeHourlyLimit:      l.GetInt("email-code-hourly-limit"),
		EmailAppName:              l.GetString("email-app-name"),
		EmailSupportAddress:       l.GetString("email-support"),
		ActivationTokens:          ar.activations,
		ActivationRequired:        l.GetBool("activation-required"),
		ActivationBaseURL:         l.GetString("activation-base-url"),
		ActivationTTL:             l.GetDuration("activation-ttl"),
	})
	if l.GetBool("register-ip-limit") {
		logger.Info("register IP limit enabled (one account per IP)",
			"allowlist_size", len(ipRegisterAllowList))
	}
	logger.Info("email verification subsystem",
		"sender", emailSenderLabel,
		"required", l.GetBool("email-verify-required"),
		"allowed_domains", len(allowedDomains),
		"blocked_domains", len(blockedDomains),
		"code_ttl", l.GetDuration("email-code-ttl"),
		"cooldown", l.GetDuration("email-code-cooldown"))
	if l.GetBool("activation-required") {
		base := l.GetString("activation-base-url")
		if base == "" {
			logger.Error("activation-required=true but -activation-base-url empty; activation will be disabled at runtime")
		} else {
			logger.Info("account activation enabled",
				"base_url", base,
				"ttl", l.GetDuration("activation-ttl"))
		}
	}

	// ---- 首次启动自动创建 superadmin（幂等）----
	bootRes, bootErr := authSvc.Bootstrap(ctx, auth.BootstrapOptions{
		Email:    l.GetString("bootstrap-email"),
		Disabled: l.GetBool("bootstrap-disable"),
		Logger:   logger,
	})
	if bootErr != nil && !errors.Is(bootErr, auth.ErrBootstrapDisabled) {
		logger.Error("auth bootstrap failed", "err", bootErr)
	}
	auth.PrintBootstrapBanner(os.Stderr, bootRes)

	// ---- Provider Registry ----
	reg := provider.NewRegistry()
	if err := qqmusicprovider.Register(reg, qqmusic.NewClient(qqmusic.ClientOptions{})); err != nil {
		return fmt.Errorf("register qqmusic provider: %w", err)
	}
	if err := netease.Register(reg, netease.NewClient(netease.ClientOptions{})); err != nil {
		return fmt.Errorf("register netease provider: %w", err)
	}

	// ---- Pool（有 PG 则持久化，否则 fallback 内存）----
	poolRepo, err := buildPoolRepo(ctx, logger, pgDSN)
	if err != nil {
		return fmt.Errorf("edge gateway: pool repo: %w", err)
	}
	psvc := pool.NewService(poolRepo, pool.Options{})

	// ---- Music Service ----
	musicSvc := music.NewService(reg, psvc, 3)

	// ---- admin token 列表 ----
	var adminTokens []string
	if raw := l.GetString("admin-token"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				adminTokens = append(adminTokens, t)
			}
		}
	}
	if len(adminTokens) == 0 {
		logger.Warn("admin tokens empty; /v1/admin/* will return 403")
	}

	// ---- Billing（支付渠道配置持久化到 PG billing.settings）----
	var billingPG *pgxpool.Pool
	if pgDSN != "" {
		if err := store.MigrateUp(ctx, migrations.FS, pgDSN, "billing"); err != nil {
			logger.Warn("billing migrations failed (non-fatal, tables may already exist)", "err", err)
		}
		// migration 失败也创建连接池（表可能已存在，仅新 migration 失败）
		if bp, err := store.NewPGPool(ctx, store.PGConfig{DSN: pgDSN}); err == nil {
			billingPG = bp
			logger.Info("billing PG connected")
		}
	}
	payReg := payment.NewRegistry()
	payConfigStore := pkggateway.NewPaymentConfigStore(billingPG, payReg)
	currencyStore := billing.NewCurrencyStore(billingPG)
	var billingRepo billing.OrderRepo
	if billingPG != nil {
		billingRepo = billing.NewPgOrderRepo(billingPG)
		logger.Info("billing orders backed by PostgreSQL")
	} else {
		billingRepo = billing.NewMemRepo()
		logger.Warn("billing: in-memory only, orders lost on restart")
	}
	billingSvc := billing.NewService(billingRepo, billing.Options{})
	billingAdapter := pkggateway.NewBillingGRPCService(billingSvc, pkggateway.NewDefaultPlanCatalog(), payReg, currencyStore)

	// ---- 用量（提前创建，注入 Server Config 做 RPM/TPM 记录）----
	var quotaPG *pgxpool.Pool
	if pgDSN != "" {
		if err := store.MigrateUp(ctx, migrations.FS, pgDSN, "quota"); err != nil {
			logger.Warn("quota migrations failed, usage history disabled", "err", err)
		} else if qp, err := store.NewPGPool(ctx, store.PGConfig{DSN: pgDSN}); err == nil {
			quotaPG = qp
			logger.Info("quota PG connected, usage history enabled")
		}
	}
	usageHandler := pkggateway.NewUsageHandler(rdb, quotaPG)

	// ---- 双协议 Server ----
	srv := pkggateway.NewServer(pkggateway.Config{
		GRPCAddr: grpcAddr,
		HTTPAddr: httpAddr,
		SessionMiddleware: &pkggateway.SessionMiddlewareOptions{
			Resolver:       authSvc,
			APIKeyVerifier: authSvc,
			SkipPaths: []string{
				"/healthz", "/readyz",
				"/v1/auth/register", "/v1/auth/login",
				"/v1/auth/totp/verify", // 登录二步：用 challenge_id 而非 session
				"/v1/auth/password/reset",
				"/v1/auth/email/verify",     // 激活：未登录用户点邮件链接调用
				"/v1/auth/email/send-code",  // 注册前的邮箱验证码：未登录场景
				"/v1/auth/email/resend",     // 重发激活邮件：登录页拦截到 412 后重发，未登录
				"/v1/auth/oauth/providers",  // 公开列表 (前端登录页渲染按钮)
				"/v1/auth/oauth/linuxdo/",   // start + callback 都在该前缀下; 未登录可访问
				"/v1/billing/callback/",
				"/v1/docs/",
				"/v1/config/", // runtime config：未登录页也要格式化日期
			},
			Required: true,
		},
		AdminAuth: &pkggateway.AdminAuthOptions{
			AdminTokens:       adminTokens,
			AcceptSessionRole: true,
		},
		UsageRecorder:  usageHandler,
		KnownProviders: reg.Names(),
	}, func(s *grpc.Server) {
		authv1.RegisterAuthServiceServer(s, pkggateway.NewAuthGRPCService(authSvc))
		musicv1.RegisterMusicServiceServer(s, pkggateway.NewMusicGRPCService(musicSvc))
		poolAdapter := pkggateway.NewPoolGRPCService(psvc)
		qqClient := qqmusic.NewClient(qqmusic.ClientOptions{})
		poolAdapter.SetRefreshFunc(pkggateway.NewQQMusicRefresher(qqClient))
		poolAdapter.RegisterRefreshFunc("qqmusic", pkggateway.NewQQMusicRefresher(qqClient))
		poolAdapter.RegisterRefreshFunc("netease", pkggateway.NewNeteaseRefresher())
		poolAdapter.SetTester(qqmusicprovider.NewProvider(qqClient))
		poolv1.RegisterPoolServiceServer(s, poolAdapter)
		// ---- Billing Service ----
		billingv1.RegisterBillingServiceServer(s, billingAdapter)
	}, func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
		if err := authv1.RegisterAuthServiceHandler(ctx, mux, conn); err != nil {
			return err
		}
		if err := musicv1.RegisterMusicServiceHandler(ctx, mux, conn); err != nil {
			return err
		}
		if err := poolv1.RegisterPoolServiceHandler(ctx, mux, conn); err != nil {
			return err
		}
		return billingv1.RegisterBillingServiceHandler(ctx, mux, conn)
	})

	// ---- 限速/配额中间件（必须在路由注册之前 Use）----
	// callbackBaseURL 是支付网关 (LDC / yipay / hupijiao) 异步回调用的公网入口。
	// 优先用 -oauth-public-base 复用 OAuth 已配置的公网域名 (HTTPS, 反代后)。
	// 没配则降级到 http://localhost:<port> 仅本地 dev 用 —— 生产忘配 OAUTH_PUBLIC_BASE
	// 时 LDC 会拿到 localhost 回调地址, 异步通知永远到不了我们, 订单卡 PENDING.
	callbackBaseURL := strings.TrimRight(l.GetString("oauth-public-base"), "/")
	if callbackBaseURL == "" {
		callbackBaseURL = "http://localhost:" + strings.TrimPrefix(httpAddr, ":")
		logger.Warn("payment callback base URL falling back to localhost; LDC / 易支付 异步通知将无法到达 gateway",
			"hint", "设置 OAUTH_PUBLIC_BASE=https://your-domain 让支付网关拿到正确的 notify_url",
			"value", callbackBaseURL)
	} else {
		logger.Info("payment callback base URL", "value", callbackBaseURL)
	}
	planRepo := pkggateway.NewPlanRepo(billingPG)
	keyRateLimiter := auth.NewKeyRateLimiter(rdb)

	// ---- Balance（用户钱包）----
	var balanceRepo pkggateway.BalanceRepo
	if billingPG != nil {
		balanceRepo = pkggateway.NewPgBalanceRepo(billingPG)
		logger.Info("balance backed by PostgreSQL")
	} else {
		balanceRepo = pkggateway.NewMemBalanceRepo()
		logger.Warn("balance in-memory only; balances WILL be lost on restart")
	}

	// ---- Extra Usage（用户级超额开关 + 余额扣费）----
	var extraUsageRepo pkggateway.ExtraUsageRepo
	if billingPG != nil {
		extraUsageRepo = pkggateway.NewPgExtraUsageRepo(billingPG)
		logger.Info("extra usage backed by PostgreSQL")
	} else {
		extraUsageRepo = pkggateway.NewMemExtraUsageRepo()
		logger.Warn("extra usage in-memory only; settings WILL be lost on restart")
	}
	extraUsageSvc := pkggateway.NewExtraUsageService(extraUsageRepo, balanceRepo)

	// 路径级 superadmin 角色门：把高敏感操作（密码强重置 / 余额账本调账 /
	// 套餐 CRUD / 全局支付与货币配置）从 admin 通用门提到 superadmin 唯一门。
	// 挂在 AdminAuthMiddleware（NewServer 内）之后、ScopeMiddleware 之前，让
	// 角色不达标的请求尽早拒绝。
	srv.Engine().Use(pkggateway.SuperadminPathMiddleware(pkggateway.SuperadminPathMiddlewareOptions{}))

	// API Key scope 校验：仅对通过 X-API-Key 认证的请求生效；
	// 挂在 RateLimit/Quota 之前，让"无 scope"早早拒绝避免浪费计数槽位。
	srv.Engine().Use(pkggateway.ScopeMiddleware(pkggateway.ScopeMiddlewareOptions{}))
	srv.Engine().Use(pkggateway.APIKeyRateLimitMiddleware(keyRateLimiter))
	srv.Engine().Use(pkggateway.PlanQuotaMiddleware(rdb, pkggateway.PlanQPSConfig{
		PlanRepo: planRepo,
		QuotaPG:  quotaPG,
		Plans: map[string]int{
			"free":       5,
			"basic":      20,
			"pro":        50,
			"enterprise": 200,
		},
		DefaultQPS: 5,
		ExtraUsage: extraUsageSvc,
	}))

	// ---- Music REST（标准化响应格式，优先于 gRPC-gateway NoRoute）----
	pkggateway.NewMusicHandler(musicSvc).Mount(srv.Engine())

	// ---- 网易云登录（REST 端点）----
	pkggateway.NewNeteaseLoginHandler().Mount(srv.Engine())

	// ---- 支付回调 + 订单管理（接入 Balance 处理充值订单履约）----
	paymentCallback := pkggateway.NewPaymentCallbackHandler(billingSvc, authSvc, payReg, planRepo, callbackBaseURL)
	paymentCallback.SetCurrencyStore(currencyStore)
	balanceHandler := pkggateway.NewBalanceHandler(balanceRepo, billingSvc, payReg, currencyStore, paymentCallback.NotifyURL)
	paymentCallback.SetBalanceHandler(balanceHandler)
	paymentCallback.Mount(srv.Engine())
	balanceHandler.Mount(srv.Engine())

	// ---- API Key 扩展管理 ----
	pkggateway.NewAPIKeyHandler(authSvc).Mount(srv.Engine())

	// ---- QR 登录管理器（admin REST 入口）----
	qrMgr := pkggateway.NewQRLoginManager(qqmusic.NewClient(qqmusic.ClientOptions{}))
	pkggateway.NewQRLoginAdminHandler(qrMgr).Mount(srv.Engine())

	usageHandler.SetPlanRepo(planRepo)
	usageHandler.Mount(srv.Engine())

	// ---- 支付渠道配置（admin REST）----
	pkggateway.NewPaymentConfigHandler(payConfigStore).Mount(srv.Engine())

	// ---- 全局货币 + 汇率配置（admin REST）----
	pkggateway.NewCurrencyHandler(currencyStore).Mount(srv.Engine())

	// ---- Extra Usage（用户级超额开关 + 报告 REST）----
	pkggateway.NewExtraUsageHandler(extraUsageRepo, planRepo, balanceRepo).Mount(srv.Engine())

	// ---- Admin Users（超级管理员用户管理：列表 / 改角色 / 调余额 / 切套餐）----
	pkggateway.NewAdminUsersHandler(authSvc, balanceRepo, planRepo).Mount(srv.Engine())

	// ---- 当前用户套餐查询（/v1/billing/me/plan）----
	pkggateway.NewUserPlanHandler(authSvc, planRepo).Mount(srv.Engine())

	// ---- OpenAPI 文档（/v1/docs/openapi.yaml）----
	pkggateway.NewDocsHandler().Mount(srv.Engine())

	// ---- Runtime Config（公开端点；前端拉取后用于本地化显示）----
	emailStatus := authSvc.EmailVerificationStatus()
	activationStatus := authSvc.AccountActivationStatus()
	pkggateway.NewRuntimeConfigHandler(pkggateway.RuntimeConfig{
		TimeZone: resolvedTZ,
		EmailVerification: pkggateway.EmailVerificationRuntime{
			Enabled:            emailStatus.Enabled,
			Required:           emailStatus.Required,
			CooldownSeconds:    emailStatus.CooldownSeconds,
			CodeTTLSeconds:     emailStatus.CodeTTLSeconds,
			HourlyLimitPerMail: emailStatus.HourlyLimitPerMail,
			AllowedDomains:     emailStatus.AllowedDomains,
			BlockedDomains:     emailStatus.BlockedDomains,
			AppName:            emailStatus.AppName,
		},
		AccountActivation: pkggateway.AccountActivationRuntime{
			Enabled:    activationStatus.Enabled,
			Required:   activationStatus.Required,
			TTLSeconds: activationStatus.TTLSeconds,
		},
	}).Mount(srv.Engine())

	// ---- OAuth (Linux.do SSO) ----
	if oauthSvc, oauthLabel := buildOAuthService(logger, l, ar.pgPool, authSvc); oauthSvc != nil {
		errorBase := strings.TrimRight(l.GetString("oauth-frontend-base"), "/") + "/oauth-error"
		if l.GetString("oauth-frontend-base") == "" {
			errorBase = "/oauth-error"
		}
		pkggateway.NewOAuthHandler(oauthSvc, authSvc,
			pkggateway.AuthCookieOptions{Secure: true},
			errorBase,
		).Mount(srv.Engine())
		logger.Info("oauth handlers mounted", "providers", oauthLabel)
	} else {
		logger.Info("oauth subsystem disabled (no provider enabled)")
	}

	logger.Info("ready", "http", httpAddr, "grpc", grpcAddr)
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("edge gateway: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

// buildUserRepo 决定用 PG / 文件 / 内存哪一种 UserRepo。
//
// PG 路径：跑 auth schema 迁移 → 构造 PgUserRepo（推荐生产）。
// 文件路径：JSON 落盘 + 启动 load（开发期跨重启）。
// 内存：仅当上面两者都为空，会发 WARN 并继续。
//
// buildOAuthService 装配 OAuth Service: KEK + IdentityRepo + ProviderRegistry + StateCodec.
//
// 行为:
//   - 任一 provider 未启用 (-oauth-linuxdo-enabled=false 等) → 返回 (nil, "")
//     由 caller 跳过 handler 挂载.
//   - PG 缺失 → fallback MemIdentityRepo (重启丢绑定; 仅 dev OK).
//   - KEK 加载失败 (env/文件都没): warn + 退化明文 (生产应该报错 — 当前简化).
//
// 返回 (svc, providersLabel); providersLabel 是 "linuxdo" 之类逗号分隔字符串, 仅日志用.
func buildOAuthService(logger *slog.Logger, l *cmdpkg.Loader, pgPool *pgxpool.Pool, authSvc *auth.Service) (*oauth.Service, string) {
	reg := oauth.NewRegistry()
	var labels []string

	publicBase := strings.TrimRight(l.GetString("oauth-public-base"), "/")
	frontendBase := strings.TrimRight(l.GetString("oauth-frontend-base"), "/")
	if frontendBase == "" {
		frontendBase = publicBase
	}

	// ---- Linux.do ----
	if l.GetBool("oauth-linuxdo-enabled") {
		clientID := l.GetString("oauth-linuxdo-client-id")
		clientSecret := l.GetString("oauth-linuxdo-client-secret")
		redirect := l.GetString("oauth-linuxdo-redirect-url")
		if redirect == "" && publicBase != "" {
			redirect = publicBase + "/v1/auth/oauth/linuxdo/callback"
		}
		if clientID == "" || clientSecret == "" {
			logger.Warn("oauth.linuxdo enabled but client_id/secret empty; provider skipped",
				"hint", "set OAUTH_LINUXDO_CLIENT_ID / OAUTH_LINUXDO_CLIENT_SECRET")
		} else if redirect == "" {
			logger.Warn("oauth.linuxdo enabled but redirect_url empty; provider skipped",
				"hint", "set OAUTH_LINUXDO_REDIRECT_URL or OAUTH_PUBLIC_BASE")
		} else {
			prov, err := oauth.NewLinuxDoProvider(oauth.LinuxDoConfig{
				ClientID:      clientID,
				ClientSecret:  clientSecret,
				RedirectURL:   redirect,
				MinTrustLevel: l.GetInt("oauth-linuxdo-min-trust-level"),
				RequireActive: l.GetBool("oauth-linuxdo-require-active"),
			})
			if err != nil {
				logger.Error("oauth.linuxdo init failed", "err", err)
			} else {
				reg.Register(prov)
				labels = append(labels, "linuxdo")
				logger.Info("oauth.linuxdo registered",
					"redirect", redirect,
					"min_trust_level", l.GetInt("oauth-linuxdo-min-trust-level"),
					"require_active", l.GetBool("oauth-linuxdo-require-active"))
			}
		}
	}

	if len(labels) == 0 {
		return nil, ""
	}

	// ---- KEK 加载 (用于加密 access/refresh token) ----
	var kek []byte
	if k, src, fresh, err := crypto.LoadOrGenerateKEK("POOL_KEK_HEX", "data/.kek"); err == nil {
		kek = k
		logger.Info("oauth: KEK loaded", "src", src, "fresh", fresh)
	} else {
		logger.Warn("oauth: KEK unavailable; tokens will be stored in plaintext (set POOL_KEK_HEX in production)",
			"err", err)
	}

	// ---- IdentityRepo (PG 优先, 否则 mem fallback) ----
	var idRepo oauth.IdentityRepo
	if pgPool != nil {
		repo, err := oauth.NewPgIdentityRepo(pgPool, kek)
		if err != nil {
			logger.Warn("oauth: PG identity repo init failed; falling back to memory", "err", err)
			idRepo = oauth.NewMemIdentityRepo()
		} else {
			idRepo = repo
			logger.Info("oauth: identity repo backed by PostgreSQL")
		}
	} else {
		idRepo = oauth.NewMemIdentityRepo()
		logger.Warn("oauth: identity repo in-memory only (no PG); bindings WILL be lost on restart")
	}

	// ---- StateCodec (用 KEK; 不可用则随机派生一段) ----
	codecSecret := kek
	if codecSecret == nil {
		// 随机生成 32B 进程级 secret; 重启后旧 cookie 全失效 (15min 内的用户重试一次即可).
		codecSecret = make([]byte, 32)
		_, _ = cryptoRandom(codecSecret)
		logger.Warn("oauth: state codec using ephemeral secret; in-flight flows will fail on restart")
	}
	codec, err := oauth.NewFlowStateCodec(codecSecret)
	if err != nil {
		logger.Error("oauth: codec init failed", "err", err)
		return nil, ""
	}

	allowedHosts := []string{}
	if frontendBase != "" {
		if u, err := url.Parse(frontendBase); err == nil && u.Host != "" {
			allowedHosts = append(allowedHosts, u.Host)
		}
	}

	svc, err := oauth.NewService(oauth.ServiceOptions{
		Auth:                   authSvc,
		Identities:             idRepo,
		Registry:               reg,
		Codec:                  codec,
		Logger:                 logger,
		AllowedNextHosts:       allowedHosts,
		SuccessRedirectDefault: defaultIfEmpty(frontendBase, "") + "/",
		BindSuccessRedirect:    defaultIfEmpty(frontendBase, "") + "/settings?bind=ok",
		ErrorRedirectBase:      defaultIfEmpty(frontendBase, "") + "/oauth-error",
	})
	if err != nil {
		logger.Error("oauth.Service init failed", "err", err)
		return nil, ""
	}
	return svc, strings.Join(labels, ",")
}

// defaultIfEmpty 返回 v; v 为空时返回 fallback.
func defaultIfEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// cryptoRandom 包装 crypto/rand.Read; 简化错误返回 (oauth-only 用).
func cryptoRandom(b []byte) (int, error) {
	return rand.Read(b)
}

// authRepos 是 buildAuthRepos 的返回值。
type authRepos struct {
	users       auth.UserRepo
	apiKeys     auth.APIKeyRepo
	activations auth.ActivationTokenStore
	label       string
	pgPool      *pgxpool.Pool // PG 模式下持有 pool, 供 OAuth 等下游复用; 文件/内存模式为 nil
}

// buildAuthRepos 构建 auth 层的 UserRepo + APIKeyRepo。
// 有 PG 时两者共享同一个连接池（search_path=auth），API Keys 持久化到 auth.api_keys 表。
func buildAuthRepos(ctx context.Context, logger *slog.Logger, pgDSN, filePath string) (authRepos, error) {
	if pgDSN != "" {
		if err := store.MigrateUp(ctx, migrations.FS, pgDSN, "auth"); err != nil {
			diag := diagnosePgError(pgDSN, err)
			logger.Error("auth migrations failed", "err", err, "hint", diag)
			return authRepos{}, fmt.Errorf("auth migrations: %s: %w", diag, err)
		}
		poolDSN, err := store.AppendSearchPath(pgDSN, "auth")
		if err != nil {
			return authRepos{}, fmt.Errorf("auth dsn: %w", err)
		}
		pgPool, err := store.NewPGPool(ctx, store.PGConfig{DSN: poolDSN})
		if err != nil {
			diag := diagnosePgError(pgDSN, err)
			logger.Error("pg pool init failed", "err", err, "hint", diag)
			return authRepos{}, fmt.Errorf("pg pool: %s: %w", diag, err)
		}
		logger.Info("auth backed by PostgreSQL", "schema", "auth")
		return authRepos{
			users:       auth.NewPgUserRepo(pgPool),
			apiKeys:     auth.NewPgAPIKeyRepo(pgPool),
			activations: auth.NewPgActivationTokenStore(pgPool),
			label:       "postgres:auth",
			pgPool:      pgPool,
		}, nil
	}
	if filePath != "" {
		repo, err := auth.NewFileUserRepo(filePath, func(werr error) {
			logger.Warn("user store persist failed", "path", filePath, "err", werr)
		})
		if err != nil {
			return authRepos{}, fmt.Errorf("file user store: %w", err)
		}
		logger.Info("user store backed by JSON file (no -pg DSN provided)", "path", filePath)
		return authRepos{
			users:       repo,
			apiKeys:     auth.NewMemAPIKeyRepo(),
			activations: auth.NewMemActivationTokenStore(),
			label:       "file:" + filePath,
		}, nil
	}
	logger.Warn("auth: in-memory only; data WILL be lost on restart. Set -pg for production.")
	return authRepos{
		users:       auth.NewMemUserRepo(),
		apiKeys:     auth.NewMemAPIKeyRepo(),
		activations: auth.NewMemActivationTokenStore(),
		label:       "memory(volatile)",
	}, nil
}

// buildPoolRepo 决定凭据池用 PG 还是内存。
// 有 PG DSN 时跑 pool 迁移 → PgRepo（持久化，重启不丢）；否则 MemRepo（开发用）。
func buildPoolRepo(ctx context.Context, logger *slog.Logger, pgDSN string) (pool.Repo, error) {
	if pgDSN == "" {
		logger.Warn("pool repo: in-memory only; credentials WILL be lost on restart. Set -pg for production.")
		return pool.NewMemRepo(), nil
	}
	if err := store.MigrateUp(ctx, migrations.FS, pgDSN, "pool"); err != nil {
		logger.Error("pool migrations failed", "err", err)
		return nil, fmt.Errorf("pool migrations: %w", err)
	}
	pgPool, err := store.NewPGPool(ctx, store.PGConfig{DSN: pgDSN})
	if err != nil {
		return nil, fmt.Errorf("pool pg connect: %w", err)
	}
	repo, err := pool.NewPgRepo(pgPool)
	if err != nil {
		return nil, fmt.Errorf("pool PgRepo: %w", err)
	}
	logger.Info("pool repo backed by PostgreSQL", "schema", "pool")
	return repo, nil
}

// diagnosePgError 把常见 PG 连接 / 认证 / 库不存在错误翻成中文修复建议。
//
// 设计目标：让用户看到 "请检查 deploy/compose/.env 的 POSTGRES_PASSWORD" 这类
// actionable 信息，而不是裸 `pq: password authentication failed for user "mgw"`。
//
// 输入 dsn 不是为了 echo 密码（绝不打印），而是用于解析出 user/host/db 让提示更具体。
func diagnosePgError(dsn string, err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	user, host, db := parsePgDSN(dsn)

	switch {
	// ---- 迁移源错误（与 PG 连接无关，必须先匹配，避免被下面的 "does not exist" 误吞）----
	case strings.Contains(msg, "no migration found for version"):
		return "schema_migrations 记录了一个 SQL 文件里不存在的版本号（典型由历史 heal bug 写入 version=0 触发）；本次启动会自动 Force(-1) 全量重跑修复，若仍报此错请人工执行：psql -c \"DELETE FROM auth.schema_migrations\" 后重启"
	case strings.Contains(msg, "dirty database version"):
		return "上次迁移中途失败留下 dirty 标记；自愈逻辑会 Force(N-1) 后重跑，若反复失败请贴出完整迁移 SQL 和对应 PG 报错"
	case strings.Contains(msg, "migrate up") && strings.Contains(msg, "file does not exist"):
		return "迁移源文件读取失败；通常是 embed FS 内容缺失或 schema 子目录名错配，请检查 gateway/migrations/embed.go"

	// ---- PG 连接 / 认证 / 库不存在 ----
	case strings.Contains(msg, "password authentication failed"):
		return fmt.Sprintf("PG 用户 %q 密码错误；请确认 -pg DSN 中的密码 == deploy/compose/.env 的 POSTGRES_PASSWORD（首次启动后改密码需要 docker compose down -v 清掉 PGDATA volume 才生效）", user)
	case strings.Contains(msg, "role") && strings.Contains(msg, "does not exist"):
		return fmt.Sprintf("PG 用户 %q 不存在；compose 默认用户名是 mgw（POSTGRES_USER），请把 DSN 用户名也改成 mgw", user)
	case strings.Contains(msg, "database") && strings.Contains(msg, "does not exist"):
		return fmt.Sprintf("PG 数据库 %q 不存在；compose 默认 DB 是 mgw（POSTGRES_DB），请把 DSN 末尾路径也改成 /mgw", db)
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no route to host"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "connect: timeout"):
		return fmt.Sprintf("PG 不可达 (%s)；请确认 docker compose ps 显示 mgw-postgres 健康 + 端口 5432 已映射到 127.0.0.1", host)
	case strings.Contains(msg, "ssl is not enabled"),
		strings.Contains(msg, "tls handshake"),
		strings.Contains(msg, "tls:"),
		strings.Contains(msg, "ssl off"):
		return "PG TLS 握手失败；本地开发请在 DSN 末尾加 ?sslmode=disable"
	case strings.Contains(msg, "pq: schema") || strings.Contains(msg, "schema \"auth\""):
		return "auth schema 不存在；compose 启动时会跑 init.sql 自动建 schema，确认 PGDATA volume 是首次启动或 docker compose down -v 重建"
	default:
		return "PG 连接或迁移失败；请贴出完整错误链路（连接/认证/迁移）+ docker compose ps + DSN 形态"
	}
}

// tzSourceLabel 用于启动日志：标明这次的时区是从配置（flag/env）来还是回退系统。
// 仅作 telemetry 和首屏可读性用；空值就回 "system"。
func tzSourceLabel(rawConfig string) string {
	if strings.TrimSpace(rawConfig) == "" {
		return "system"
	}
	return "config"
}

// parsePgDSN 从 DSN 提取 user / host / db 用于诊断；解析失败返回空串。
// 不返回密码，避免无意打印。
func parsePgDSN(dsn string) (user, host, db string) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", "", ""
	}
	// url.Parse 对无 scheme 的字符串很宽松（"not a url" → path="not a url"），
	// 这里强约束 scheme 必须是 postgres / postgresql 才认为是有效 DSN。
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", "", ""
	}
	if u.User != nil {
		user = u.User.Username()
	}
	host = u.Host
	db = strings.TrimPrefix(u.Path, "/")
	return
}
