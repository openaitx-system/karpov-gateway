package gateway

import (
	"log/slog"
	"strings"

	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	"github.com/MiChongs/QQMusicApi/gateway/internal/email"
)

// splitCSV 把 "a, b , c" 切成 ["a","b","c"]。空段去掉，整体修剪。
//
// 跨多个 flag 复用（admin-token / register-ip-allowlist / email-*-domains）；
// 因此放在文件级而不是 Run 函数内的局部 lambda。
func splitCSV(s string) []string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildEmailSender 根据 flag 决定使用真实 SMTPSender 还是 LogSender（dev fallback）。
//
// 规则：
//   - smtp-host 非空 + smtp-from 非空 ⇒ 尝试 NewSMTPSender；失败时降级 LogSender 并 warn，
//     不让 SMTP 误配置阻塞整个网关启动（管理员可在 banner 看到 sender_label = "smtp_failed"）。
//   - 否则 ⇒ LogSender；管理员能在 stdout 看到验证码，方便本地 dev 不接 MailHog 也能跑通注册流。
//
// 第二返回值是给 logger 展示的标签（"smtp" / "smtp_failed_log_fallback" / "log"）。
func buildEmailSender(logger *slog.Logger, l *cmdpkg.Loader) (email.Sender, string) {
	host := strings.TrimSpace(l.GetString("smtp-host"))
	from := strings.TrimSpace(l.GetString("smtp-from"))
	fromName := strings.TrimSpace(l.GetString("smtp-from-name"))

	if host == "" {
		// 没配 SMTP：用 LogSender 让 dev 仍能完整跑流程。
		logger.Warn("SMTP not configured; using LogSender (codes printed to stdout, no real email sent)")
		return email.NewLogSender(orFallback(from, "noreply@dev.local"), logger), "log"
	}
	if from == "" {
		logger.Error("smtp-from required when smtp-host is set; falling back to LogSender")
		return email.NewLogSender("noreply@dev.local", logger), "smtp_misconfigured_log_fallback"
	}

	cfg := email.SMTPConfig{
		Host:         host,
		Port:         l.GetInt("smtp-port"),
		Username:     l.GetString("smtp-user"),
		Password:     l.GetString("smtp-password"),
		FromAddress:  from,
		FromName:     fromName,
		TLS:          email.TLSMode(strings.ToLower(strings.TrimSpace(l.GetString("smtp-tls")))),
		InsecureSkip: l.GetBool("smtp-tls-insecure"),
		Timeout:      l.GetDuration("smtp-timeout"),
		Logger:       logger,
	}
	sender, err := email.NewSMTPSender(cfg)
	if err != nil {
		logger.Error("SMTP sender init failed; falling back to LogSender", "err", err)
		return email.NewLogSender(from, logger), "smtp_failed_log_fallback"
	}
	logger.Info("SMTP sender ready",
		"host", cfg.Host, "port", cfg.Port,
		"from", cfg.FromAddress, "tls", cfg.TLS,
		"auth", cfg.Username != "")
	return sender, "smtp"
}

// orFallback returns a if non-empty else b.
func orFallback(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
