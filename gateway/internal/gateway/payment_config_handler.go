package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing/payment"
)

// PaymentChannelConfig 是单个支付渠道的配置。
type PaymentChannelConfig struct {
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`

	YipayBaseURL     string `json:"yipayBaseUrl,omitempty"`
	YipayMerchantID  string `json:"yipayMerchantId,omitempty"`
	YipayKey         string `json:"yipayKey,omitempty"`
	YipayPaymentType string `json:"yipayPaymentType,omitempty"`

	HupijiaoBaseURL string `json:"hupijiaoBaseUrl,omitempty"`
	HupijiaoAppID   string `json:"hupijiaoAppId,omitempty"`
	HupijiaoKey     string `json:"hupijiaoKey,omitempty"`
	HupijiaoWapName string `json:"hupijiaoWapName,omitempty"`

	// Linux Credit (linux.do) 官方 LDC 协议（type=ldcpay，Ed25519 签名）。
	LDCBaseURL            string `json:"ldcBaseUrl,omitempty"`            // 默认 https://credit.linux.do/epay
	LDCClientID           string `json:"ldcClientId,omitempty"`           // 控制台 client_id
	LDCClientSecret       string `json:"ldcClientSecret,omitempty"`       // 控制台 client_secret（敏感，回显时脱敏）
	LDCMerchantPrivateKey string `json:"ldcMerchantPrivateKey,omitempty"` // PEM / base64-32 / base64-64（敏感，回显时脱敏）
	LDCPlatformPublicKey  string `json:"ldcPlatformPublicKey,omitempty"`  // PEM 或 base64-32；用于回调验签，可空
}

const paymentSettingsKey = "payment_channels"

// PaymentConfigStore 管理支付渠道配置——PG 持久化 + 热更新 Registry。
type PaymentConfigStore struct {
	mu       sync.RWMutex
	pg       *pgxpool.Pool
	registry *payment.Registry
	channels []PaymentChannelConfig
}

// NewPaymentConfigStore 构造配置管理器，从 PG billing.settings 加载。
func NewPaymentConfigStore(pg *pgxpool.Pool, registry *payment.Registry) *PaymentConfigStore {
	s := &PaymentConfigStore{pg: pg, registry: registry}
	s.loadFromPG()
	s.applyToRegistry()
	return s
}

func (s *PaymentConfigStore) loadFromPG() {
	if s.pg == nil {
		s.channels = defaultChannels()
		return
	}
	var raw []byte
	err := s.pg.QueryRow(context.Background(),
		`SELECT value FROM billing.settings WHERE key = $1`, paymentSettingsKey).Scan(&raw)
	if err != nil {
		s.channels = defaultChannels()
		return
	}
	var channels []PaymentChannelConfig
	if json.Unmarshal(raw, &channels) != nil {
		s.channels = defaultChannels()
		return
	}
	s.channels = channels
}

func (s *PaymentConfigStore) saveToPG() error {
	if s.pg == nil {
		return nil
	}
	data, err := json.Marshal(s.channels)
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO billing.settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = now()
	`
	_, err = s.pg.Exec(context.Background(), q, paymentSettingsKey, data)
	return err
}

func (s *PaymentConfigStore) applyToRegistry() {
	_ = s.registry.Register(payment.NewMock())
	for _, ch := range s.channels {
		if !ch.Enabled {
			continue
		}
		switch ch.Provider {
		case "yipay":
			if ch.YipayBaseURL != "" && ch.YipayMerchantID != "" && ch.YipayKey != "" {
				_ = s.registry.Register(payment.NewYipay(payment.YipayConfig{
					BaseURL:     ch.YipayBaseURL,
					MerchantID:  ch.YipayMerchantID,
					Key:         ch.YipayKey,
					PaymentType: ch.YipayPaymentType,
				}))
			}
		case "hupijiao":
			if ch.HupijiaoBaseURL != "" && ch.HupijiaoAppID != "" && ch.HupijiaoKey != "" {
				_ = s.registry.Register(payment.NewHupijiao(payment.HupijiaoConfig{
					BaseURL: ch.HupijiaoBaseURL,
					AppID:   ch.HupijiaoAppID,
					Key:     ch.HupijiaoKey,
					WapName: ch.HupijiaoWapName,
				}))
			}
		case "ldcpay":
			if ch.LDCClientID == "" || ch.LDCClientSecret == "" || ch.LDCMerchantPrivateKey == "" {
				slog.Warn("payment.ldc: enabled but credentials incomplete; not registering",
					"hasClientID", ch.LDCClientID != "",
					"hasClientSecret", ch.LDCClientSecret != "",
					"hasPrivateKey", ch.LDCMerchantPrivateKey != "")
				continue
			}
			priv, err := payment.ParseEd25519PrivateKey(ch.LDCMerchantPrivateKey)
			if err != nil {
				slog.Warn("payment.ldc: skip channel due to bad private key",
					"err", err)
				continue
			}
			cfg := payment.LDCConfig{
				BaseURL:            ch.LDCBaseURL,
				ClientID:           ch.LDCClientID,
				ClientSecret:       ch.LDCClientSecret,
				MerchantPrivateKey: priv,
			}
			if ch.LDCPlatformPublicKey != "" {
				pub, err := payment.ParseEd25519PublicKey(ch.LDCPlatformPublicKey)
				if err != nil {
					slog.Warn("payment.ldc: invalid platform public key, Ed25519 callbacks will be rejected",
						"err", err)
				} else {
					cfg.PlatformPublicKey = pub
				}
			}
			// 平台公钥未配置不算异常：LDC 默认用 MD5+client_secret 签回调，
			// VerifyCallback 会自适应识别签名方式（详见 ldc.go）。
			// 仅当真有 Ed25519 回调进来又没公钥时才会按拒签处理并打 WARN。
			ldc, err := payment.NewLDC(cfg)
			if err != nil {
				slog.Warn("payment.ldc: NewLDC failed", "err", err)
				continue
			}
			_ = s.registry.Register(ldc)
		}
	}
}

func (s *PaymentConfigStore) Get() []PaymentChannelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PaymentChannelConfig, len(s.channels))
	for i, ch := range s.channels {
		out[i] = ch
		out[i].YipayKey = maskSecret(ch.YipayKey)
		out[i].HupijiaoKey = maskSecret(ch.HupijiaoKey)
		out[i].LDCClientSecret = maskSecret(ch.LDCClientSecret)
		out[i].LDCMerchantPrivateKey = maskSecret(ch.LDCMerchantPrivateKey)
		// 平台公钥不算敏感，不脱敏
	}
	return out
}

func (s *PaymentConfigStore) Update(channels []PaymentChannelConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ch := range channels {
		if isMasked(ch.YipayKey) {
			channels[i].YipayKey = s.findOldKey("yipay", func(c PaymentChannelConfig) string { return c.YipayKey })
		}
		if isMasked(ch.HupijiaoKey) {
			channels[i].HupijiaoKey = s.findOldKey("hupijiao", func(c PaymentChannelConfig) string { return c.HupijiaoKey })
		}
		if isMasked(ch.LDCClientSecret) {
			channels[i].LDCClientSecret = s.findOldKey("ldcpay", func(c PaymentChannelConfig) string { return c.LDCClientSecret })
		}
		if isMasked(ch.LDCMerchantPrivateKey) {
			channels[i].LDCMerchantPrivateKey = s.findOldKey("ldcpay", func(c PaymentChannelConfig) string { return c.LDCMerchantPrivateKey })
		}
	}
	s.channels = channels
	if err := s.saveToPG(); err != nil {
		return err
	}
	s.applyToRegistry()
	return nil
}

func (s *PaymentConfigStore) findOldKey(provider string, getter func(PaymentChannelConfig) string) string {
	for _, ch := range s.channels {
		if ch.Provider == provider {
			return getter(ch)
		}
	}
	return ""
}

func defaultChannels() []PaymentChannelConfig {
	return []PaymentChannelConfig{
		{Provider: "yipay", Enabled: false, YipayPaymentType: "alipay"},
		{Provider: "hupijiao", Enabled: false},
		{Provider: "ldcpay", Enabled: false, LDCBaseURL: "https://credit.linux.do/epay"},
	}
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}

func isMasked(s string) bool {
	if s == "" {
		return false
	}
	if s == "****" {
		return true
	}
	return len(s) >= 8 && s[2:len(s)-2] == "****"
}

// PaymentChannelPublic 是返回给"用户端"的支付渠道展示信息。
// 不包含任何敏感字段（密钥、私钥），只用于在下单 / 充值对话框里渲染选项。
type PaymentChannelPublic struct {
	Provider    string `json:"provider"`              // 内部标识：mock / yipay / hupijiao / ldcpay
	DisplayName string `json:"displayName"`           // 用户可读的中文名
	Description string `json:"description,omitempty"` // 选项下的小字说明
	Icon        string `json:"icon,omitempty"`        // 前端用以选图标：alipay/wechat/credit/test/generic
}

// channelDisplay 内置 provider → 展示信息映射。改动这里即可更新所有用户端文案。
//
// 注意：mock 不在此处 —— 测试支付不暴露给用户端，按需求"全局去除测试支付"。
// mock 仍由 applyToRegistry 注册到 Registry 以便单测使用，但不会出现在
// /v1/billing/payment-channels 的返回里。
var channelDisplay = map[string]struct {
	DisplayName string
	Description string
	Icon        string
}{
	"yipay":    {"支付宝 / 微信（易支付）", "支持支付宝、微信、QQ 钱包", "alipay"},
	"hupijiao": {"微信支付（虎皮椒）", "微信个人收款方案", "wechat"},
	"ldcpay":   {"Linux Credit (LDC)", "linux.do 官方积分流转，Ed25519 签名", "credit"},
}

// PublicChannels 返回当前已启用且配置完整的渠道（脱敏，可安全暴露给用户端）。
//
// 规则：
//   - 只暴露 channelDisplay 表中登记的 provider（即不含 mock）
//   - 必须 enabled=true 且关键字段都已填好
//   - 配齐字段但被 enabled 的渠道在配置不完整时会打 WARN，方便管理员定位
//   - 开发开关：env GATEWAY_EXPOSE_MOCK_PAYMENT=1 时，把 mock 加回列表（仅本地用）
func (s *PaymentConfigStore) PublicChannels() []PaymentChannelPublic {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]PaymentChannelPublic, 0, len(s.channels)+1)
	for _, ch := range s.channels {
		if !ch.Enabled {
			continue
		}
		meta, displayed := channelDisplay[ch.Provider]
		if !displayed {
			// 不在白名单（如 mock 或未来未知 provider）—— 不暴露
			continue
		}
		if !channelReady(ch) {
			slog.Warn("payment: channel enabled but config incomplete, hiding from user UI",
				"provider", ch.Provider,
				"reason", channelReadyMissing(ch))
			continue
		}
		out = append(out, PaymentChannelPublic{
			Provider:    ch.Provider,
			DisplayName: meta.DisplayName,
			Description: meta.Description,
			Icon:        meta.Icon,
		})
	}

	// 开发本地：env 打开后追加 mock，让"测试支付"重新可见。
	// 该 env 不应在生产环境配置；不设/设为 0 时严格隐藏。
	if mockExposedForDev() {
		out = append(out, PaymentChannelPublic{
			Provider:    "mock",
			DisplayName: "本地测试支付（仅开发）",
			Description: "立即标记成功，不经真实支付网关；生产环境不会出现",
			Icon:        "test",
		})
	}
	return out
}

// mockExposedForDev 判断当前进程是否处于"本地开发"模式：
// 仅当 env GATEWAY_EXPOSE_MOCK_PAYMENT 显式置为真值时返回 true。
func mockExposedForDev() bool {
	v := strings.TrimSpace(os.Getenv("GATEWAY_EXPOSE_MOCK_PAYMENT"))
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// channelReadyMissing 返回缺失字段的简短描述，仅用于日志。
func channelReadyMissing(ch PaymentChannelConfig) string {
	switch ch.Provider {
	case "yipay":
		var miss []string
		if ch.YipayBaseURL == "" {
			miss = append(miss, "yipayBaseUrl")
		}
		if ch.YipayMerchantID == "" {
			miss = append(miss, "yipayMerchantId")
		}
		if ch.YipayKey == "" {
			miss = append(miss, "yipayKey")
		}
		return joinMissing(miss)
	case "hupijiao":
		var miss []string
		if ch.HupijiaoBaseURL == "" {
			miss = append(miss, "hupijiaoBaseUrl")
		}
		if ch.HupijiaoAppID == "" {
			miss = append(miss, "hupijiaoAppId")
		}
		if ch.HupijiaoKey == "" {
			miss = append(miss, "hupijiaoKey")
		}
		return joinMissing(miss)
	case "ldcpay":
		var miss []string
		if ch.LDCClientID == "" {
			miss = append(miss, "ldcClientId")
		}
		if ch.LDCClientSecret == "" {
			miss = append(miss, "ldcClientSecret")
		}
		if ch.LDCMerchantPrivateKey == "" {
			miss = append(miss, "ldcMerchantPrivateKey")
		}
		return joinMissing(miss)
	}
	return "unknown provider"
}

func joinMissing(miss []string) string {
	if len(miss) == 0 {
		return "ok"
	}
	out := "missing: "
	for i, m := range miss {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}

// channelReady 判断"配置完整可用"。与 applyToRegistry 中的注册条件保持一致。
func channelReady(ch PaymentChannelConfig) bool {
	switch ch.Provider {
	case "yipay":
		return ch.YipayBaseURL != "" && ch.YipayMerchantID != "" && ch.YipayKey != ""
	case "hupijiao":
		return ch.HupijiaoBaseURL != "" && ch.HupijiaoAppID != "" && ch.HupijiaoKey != ""
	case "ldcpay":
		return ch.LDCClientID != "" && ch.LDCClientSecret != "" && ch.LDCMerchantPrivateKey != ""
	}
	return false
}

// PaymentConfigHandler 同时挂"管理员配置"与"用户端列举"两套端点。
type PaymentConfigHandler struct {
	store *PaymentConfigStore
}

func NewPaymentConfigHandler(store *PaymentConfigStore) *PaymentConfigHandler {
	return &PaymentConfigHandler{store: store}
}

func (h *PaymentConfigHandler) Mount(r *gin.Engine) {
	// 管理员：完整配置（含已脱敏的密钥）
	r.GET("/v1/admin/settings/payment", h.get)
	r.PUT("/v1/admin/settings/payment", h.put)
	// 用户端：登录用户下单时拉取可用渠道（无密钥）
	r.GET("/v1/billing/payment-channels", h.listPublic)
}

func (h *PaymentConfigHandler) get(c *gin.Context) {
	OK(c, map[string]any{"channels": h.store.Get()})
}

func (h *PaymentConfigHandler) put(c *gin.Context) {
	if role, _ := c.Get("auth.role"); role != auth.RoleSuperAdmin {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	var body struct {
		Channels []PaymentChannelConfig `json:"channels"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body")
		return
	}
	if err := h.store.Update(body.Channels); err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	OK(c, map[string]any{"channels": h.store.Get()})
}

// listPublic 返回用户端可见的支付渠道。需登录（其他 /v1/billing/* 也都需登录）。
func (h *PaymentConfigHandler) listPublic(c *gin.Context) {
	if authUserID(c) == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	OK(c, map[string]any{"channels": h.store.PublicChannels()})
}
