package gateway

import (
	"github.com/gin-gonic/gin"
)

// RuntimeConfig 是给前端用的运行时只读配置。
//
// 设计原则：
//   - 完全公开：不含任何密钥 / 内部地址 / 用户私有信息，可以匿名拉取。
//   - 单文档：前端只需要一次 GET，所有跨页面共享的 ambient 配置（时区、版本号等）
//     都从这里读，避免每个面板自己 wire 一份。
//   - 不可写：runtime 配置由部署侧通过 flag/env 决定；admin UI 不能改。
type RuntimeConfig struct {
	// TimeZone 是后端配置的全局时区（IANA 名，如 "Asia/Shanghai" / "UTC" / "Local"）。
	// "Local" 表示后端跟随系统 TZ，前端可以再 fallback 到浏览器 TZ 显示。
	TimeZone string `json:"timezone"`

	// EmailVerification 暴露邮箱验证子系统状态；前端用它决定是否在注册表单显示
	// "发送验证码"按钮、是否启用域名后缀提示、按 cooldown_seconds 渲染倒计时。
	//
	// enabled = false ⇒ 隐藏全部 email-code UI；前端只展示 email + password。
	EmailVerification EmailVerificationRuntime `json:"email_verification"`

	// AccountActivation 暴露"注册后邮件链接激活"子系统状态。
	// required=true ⇒ 注册成功后跳"已发送激活邮件"页而不是登录页；
	// 登录被拒为 412 时前端展示"未激活"+"重发激活邮件"按钮。
	AccountActivation AccountActivationRuntime `json:"account_activation"`
}

// EmailVerificationRuntime 是 RuntimeConfig 内嵌邮箱子系统状态。
type EmailVerificationRuntime struct {
	Enabled            bool     `json:"enabled"`
	Required           bool     `json:"required"`
	CooldownSeconds    int      `json:"cooldown_seconds"`
	CodeTTLSeconds     int      `json:"code_ttl_seconds"`
	HourlyLimitPerMail int      `json:"hourly_limit_per_email"`
	AllowedDomains     []string `json:"allowed_domains"`
	BlockedDomains     []string `json:"blocked_domains"`
	AppName            string   `json:"app_name,omitempty"`
}

// AccountActivationRuntime 是 RuntimeConfig 内嵌激活子系统状态。
type AccountActivationRuntime struct {
	Enabled    bool `json:"enabled"`
	Required   bool `json:"required"`
	TTLSeconds int  `json:"ttl_seconds"`
}

// RuntimeConfigHandler 暴露 GET /v1/config/runtime。
//
// 路由前缀放在 /v1/config/* 而非 /v1/admin/* 是因为它面向"任何"前端用户
// （未登录页也需要它做日期格式化）。SkipPaths 已经放过 /v1/auth/* + /v1/docs，
// 这里再加一条 /v1/config/。
type RuntimeConfigHandler struct {
	cfg RuntimeConfig
}

// NewRuntimeConfigHandler 构造 handler。
func NewRuntimeConfigHandler(cfg RuntimeConfig) *RuntimeConfigHandler {
	if cfg.TimeZone == "" {
		cfg.TimeZone = "Local"
	}
	// 显式把 nil 切片归一化为 []，让 JSON 输出 [] 而不是 null（前端 .map 不会炸）
	if cfg.EmailVerification.AllowedDomains == nil {
		cfg.EmailVerification.AllowedDomains = []string{}
	}
	if cfg.EmailVerification.BlockedDomains == nil {
		cfg.EmailVerification.BlockedDomains = []string{}
	}
	return &RuntimeConfigHandler{cfg: cfg}
}

// Mount 把 GET /v1/config/runtime 挂到 gin engine。
func (h *RuntimeConfigHandler) Mount(r *gin.Engine) {
	r.GET("/v1/config/runtime", h.get)
}

func (h *RuntimeConfigHandler) get(c *gin.Context) {
	OK(c, h.cfg)
}
