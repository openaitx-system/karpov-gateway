// Package email 提供 SMTP 发送抽象 + 验证码邮件模板渲染。
//
// 设计原则：
//   - Sender 是接口；上层 auth.Service 不直接 import net/smtp。
//   - 多种实现：SMTPSender（生产）/ LogSender（dev 无 SMTP 时把验证码打到 stdout，
//     便于本地调试）/ MockSender（单测）。
//   - 模板：HTML + plaintext multipart，带响应式样式 + 国密邮箱（QQ/163）友好渲染。
//   - 中文友好：Subject 用 base64 RFC 2047 encoded-word；Body 用 UTF-8。
package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Message 是一封待发送邮件的不可变描述。
//
// 同时携带 HTML + plaintext，自动按 multipart/alternative 组装；任一为空则只发对应那部分。
type Message struct {
	To       string // 收件人地址（暂不支持多个；M19 增加群发再说）
	Subject  string // 主题；包含中文时由 encodeSubject 自动 base64 编码
	HTMLBody string // text/html 正文
	TextBody string // text/plain 正文（备用，spam-filter 友好）
	// FromOverride 可选：覆盖 SMTPConfig.FromAddress；通常不用，仅在分品牌发件时设置。
	FromOverride string
}

// Sender 是邮件发送接口。失败必须返回 error；调用方负责重试 / 降级策略。
type Sender interface {
	Send(ctx context.Context, msg Message) error
	// FromAddress 返回 sender 默认的 From 邮箱（用于日志展示，不参与发送）。
	FromAddress() string
}

// ---- SMTP 实现 ----

// TLSMode 控制 SMTP 连接加密方式。
type TLSMode string

const (
	// TLSAuto 不加密直连（端口 25）；如果服务器宣告 STARTTLS 则升级为 TLS。
	// 默认值；适用于内网/Docker 网络中的 SMTP relay。
	TLSAuto TLSMode = "auto"
	// TLSStartTLS 强制 STARTTLS（端口 587）。连接后必须升级，否则失败。
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit 一开始就 TLS（端口 465）。
	TLSImplicit TLSMode = "ssl"
	// TLSNone 完全不加密。仅用于 MailHog / dev 环境。
	TLSNone TLSMode = "none"
)

// SMTPConfig 是 SMTP 发送配置。
//
// 必填：Host + Port + FromAddress。
// Username/Password 可空（部分 relay 在 LAN 内不要求 AUTH）。
type SMTPConfig struct {
	Host         string
	Port         int
	Username     string
	Password     string
	FromAddress  string  // 例如 "noreply@example.com"
	FromName     string  // 例如 "QQMusicGateway"；空 = 仅地址
	TLS          TLSMode // 连接加密方式；默认 TLSAuto
	InsecureSkip bool    // 测试用：跳过证书校验（生产请勿开）
	Timeout      time.Duration
	Logger       *slog.Logger
}

// SMTPSender 用 net/smtp 实现 Sender。
type SMTPSender struct {
	cfg SMTPConfig
	log *slog.Logger
}

// NewSMTPSender 校验配置并构造 sender。Host/Port/FromAddress 缺失返回 error。
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" {
		return nil, errors.New("email: smtp host empty")
	}
	if cfg.Port <= 0 {
		return nil, errors.New("email: smtp port invalid")
	}
	if cfg.FromAddress == "" {
		return nil, errors.New("email: from address empty")
	}
	if cfg.TLS == "" {
		cfg.TLS = TLSAuto
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SMTPSender{cfg: cfg, log: logger}, nil
}

// FromAddress 实现 Sender。
func (s *SMTPSender) FromAddress() string { return s.cfg.FromAddress }

// Send 实现 Sender。
//
// 流程：dial → (可选) STARTTLS / 隐式 TLS → AUTH → MAIL FROM / RCPT TO / DATA → QUIT。
// 失败立即返回；不在本层做重试，由 caller 决定。
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if msg.To == "" {
		return errors.New("email: empty To")
	}
	if msg.Subject == "" {
		return errors.New("email: empty Subject")
	}
	if msg.HTMLBody == "" && msg.TextBody == "" {
		return errors.New("email: empty body")
	}
	from := msg.FromOverride
	if from == "" {
		from = s.cfg.FromAddress
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	deadlineCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	var (
		conn net.Conn
		err  error
	)
	switch s.cfg.TLS {
	case TLSImplicit:
		tlsCfg := &tls.Config{ServerName: s.cfg.Host, InsecureSkipVerify: s.cfg.InsecureSkip} //nolint:gosec
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	default:
		conn, err = dialer.DialContext(deadlineCtx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := deadlineCtx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("email: new smtp client: %w", err)
	}
	defer func() { _ = c.Quit() }()

	// STARTTLS：auto 模式下若服务器宣告则升级；starttls 模式强制升级。
	if s.cfg.TLS == TLSStartTLS || s.cfg.TLS == TLSAuto {
		if ok, _ := c.Extension("STARTTLS"); ok {
			tlsCfg := &tls.Config{ServerName: s.cfg.Host, InsecureSkipVerify: s.cfg.InsecureSkip} //nolint:gosec
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("email: starttls: %w", err)
			}
		} else if s.cfg.TLS == TLSStartTLS {
			return errors.New("email: server does not support STARTTLS but mode=starttls")
		}
	}

	// AUTH：空账号跳过；优先 PLAIN，兼容 LOGIN（部分国内服务器只支持 LOGIN）。
	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			// PLAIN 失败时尝试 LOGIN
			if loginAuth := newLoginAuth(s.cfg.Username, s.cfg.Password); loginAuth != nil {
				if err2 := c.Auth(loginAuth); err2 != nil {
					return fmt.Errorf("email: auth (plain=%v, login=%v)", err, err2)
				}
			} else {
				return fmt.Errorf("email: auth: %w", err)
			}
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("email: RCPT TO %s: %w", msg.To, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	body := assembleMIME(from, s.cfg.FromName, msg)
	if _, err := w.Write([]byte(body)); err != nil {
		_ = w.Close()
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close DATA: %w", err)
	}
	s.log.Info("email sent", "to", msg.To, "subject", msg.Subject)
	return nil
}

// assembleMIME 拼装一封 RFC 5322 / MIME multipart/alternative 邮件。
//
// 同时给出 HTML + plaintext，邮件客户端按偏好渲染（spam-filter 也喜欢有 plaintext）。
// 中文 Subject 用 RFC 2047 encoded-word（=?UTF-8?B?...?=）。
func assembleMIME(fromAddr, fromName string, msg Message) string {
	var b strings.Builder
	if fromName != "" {
		fmt.Fprintf(&b, "From: %s <%s>\r\n", encodeHeader(fromName), fromAddr)
	} else {
		fmt.Fprintf(&b, "From: %s\r\n", fromAddr)
	}
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")

	if msg.HTMLBody != "" && msg.TextBody != "" {
		boundary := "mgw-" + randomBoundary()
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
		b.WriteString("\r\n")
		// text/plain part
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.TextBody)
		b.WriteString("\r\n")
		// text/html part
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.HTMLBody)
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "--%s--\r\n", boundary)
	} else if msg.HTMLBody != "" {
		b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.HTMLBody)
	} else {
		b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.TextBody)
	}
	return b.String()
}

// ---- LogSender / MockSender ----

// LogSender 用于 dev 环境无 SMTP 时把邮件内容打到 stdout（含验证码）。
//
// 用途：本地 docker compose 没起 MailHog 时仍能完整测注册流程；测试人员
// 直接看 server log 拿验证码即可。绝不可用在 staging/prod。
type LogSender struct {
	from string
	log  *slog.Logger
}

// NewLogSender 构造一个把邮件转 logger.Info 的 sender。
func NewLogSender(from string, logger *slog.Logger) *LogSender {
	if logger == nil {
		logger = slog.Default()
	}
	if from == "" {
		from = "noreply@dev.local"
	}
	return &LogSender{from: from, log: logger}
}

// Send 实现 Sender；不真发邮件，仅日志记录。
func (s *LogSender) Send(_ context.Context, msg Message) error {
	s.log.Info("[email-log-sender] would send",
		"from", s.from,
		"to", msg.To,
		"subject", msg.Subject,
		"text_body", msg.TextBody)
	return nil
}

// FromAddress 实现 Sender。
func (s *LogSender) FromAddress() string { return s.from }

// MockSender 用于单测：把全部 Send 调用记录在 Sent 切片。
type MockSender struct {
	mu   sync.Mutex
	from string
	Sent []Message
	// FailNext 不为零时：下次 Send 直接返回该错误；用于覆盖错误路径。
	FailNext error
}

// NewMockSender 构造一个空 MockSender。
func NewMockSender(from string) *MockSender {
	if from == "" {
		from = "test@example.com"
	}
	return &MockSender{from: from}
}

// Send 实现 Sender；记录 msg 副本到 Sent。
func (s *MockSender) Send(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.FailNext; err != nil {
		s.FailNext = nil
		return err
	}
	s.Sent = append(s.Sent, msg)
	return nil
}

// FromAddress 实现 Sender。
func (s *MockSender) FromAddress() string { return s.from }

// Last 返回最后一封发送的邮件，若无则返回 false。
func (s *MockSender) Last() (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Sent) == 0 {
		return Message{}, false
	}
	return s.Sent[len(s.Sent)-1], true
}

// Reset 清空已记录的邮件。
func (s *MockSender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sent = nil
	s.FailNext = nil
}
