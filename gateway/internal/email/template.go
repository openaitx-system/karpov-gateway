package email

import (
	"bytes"
	"embed"
	"fmt"
	htmltpl "html/template"
	texttpl "text/template"
	"time"
)

//go:embed templates/*.html templates/*.txt
var tplFS embed.FS

// VerificationCodeData 是 verification_code.{html,txt} 模板的渲染数据。
type VerificationCodeData struct {
	Code           string // 6 位数字
	ExpiresMinutes int    // 验证码有效分钟
	AppName        string // 顶部品牌名
	SubjectTitle   string // 邮件主题文案，例如 "邮箱验证码"
	Greeting       string // 一句话问候，例如 "你正在注册一个新账号，"
	SupportEmail   string // 客服邮箱；空时模板自动隐藏
	Year           int    // 页脚显示年份；空 = 自动当前年
}

// ActivationLinkData 是 activation_link.{html,txt} 模板的渲染数据。
type ActivationLinkData struct {
	UserDisplay  string // 用户邮箱（或将来的昵称）；模板做"欢迎 X"显示用
	ActivateURL  string // 完整 URL，含 token query；前端会读 ?token=xxx 调 API
	ExpiresHours int    // 链接有效小时数
	AppName      string
	SupportEmail string
	Year         int
}

// Renderer 缓存解析后的 HTML / 纯文本模板，复用减少每次邮件的解析开销。
//
// embed.FS 在 binary 内只读，所以 Renderer 是无状态的；并发调用安全。
type Renderer struct {
	html *htmltpl.Template
	text *texttpl.Template
}

// NewRenderer 解析全部模板。失败说明 embed 内容损坏（罕见）；调用方应在启动期 panic。
func NewRenderer() (*Renderer, error) {
	html, err := htmltpl.ParseFS(tplFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("email: parse html templates: %w", err)
	}
	text, err := texttpl.ParseFS(tplFS, "templates/*.txt")
	if err != nil {
		return nil, fmt.Errorf("email: parse text templates: %w", err)
	}
	return &Renderer{html: html, text: text}, nil
}

// MustNewRenderer 是 NewRenderer 的 panic 变体；用于 init / wiring 期 boilerplate。
func MustNewRenderer() *Renderer {
	r, err := NewRenderer()
	if err != nil {
		panic(err)
	}
	return r
}

// RenderVerificationCode 渲染验证码邮件 HTML + 纯文本，返回 Message（不含 To）。
//
// To 由调用方在外层填充。HTMLBody 和 TextBody 同时给出，让 Sender 自己选 multipart。
func (r *Renderer) RenderVerificationCode(d VerificationCodeData) (Message, error) {
	if d.Code == "" {
		return Message{}, fmt.Errorf("email: empty code")
	}
	if d.ExpiresMinutes <= 0 {
		d.ExpiresMinutes = 10
	}
	if d.AppName == "" {
		d.AppName = "Karpov"
	}
	if d.SubjectTitle == "" {
		d.SubjectTitle = "邮箱验证"
	}
	if d.Greeting == "" {
		d.Greeting = "你正在验证邮箱地址，"
	}
	if d.Year == 0 {
		d.Year = time.Now().Year()
	}

	var htmlBuf, textBuf bytes.Buffer
	if err := r.html.ExecuteTemplate(&htmlBuf, "verification_code.html", d); err != nil {
		return Message{}, fmt.Errorf("email: render html: %w", err)
	}
	if err := r.text.ExecuteTemplate(&textBuf, "verification_code.txt", d); err != nil {
		return Message{}, fmt.Errorf("email: render text: %w", err)
	}
	return Message{
		Subject:  fmt.Sprintf("%s 邮箱验证码 %s", d.AppName, d.Code),
		HTMLBody: htmlBuf.String(),
		TextBody: textBuf.String(),
	}, nil
}

// RenderActivationLink 渲染激活邮件 HTML + 纯文本，返回 Message（不含 To）。
func (r *Renderer) RenderActivationLink(d ActivationLinkData) (Message, error) {
	if d.ActivateURL == "" {
		return Message{}, fmt.Errorf("email: empty activate url")
	}
	if d.ExpiresHours <= 0 {
		d.ExpiresHours = 24
	}
	if d.AppName == "" {
		d.AppName = "Karpov"
	}
	if d.UserDisplay == "" {
		d.UserDisplay = "你好"
	}
	if d.Year == 0 {
		d.Year = time.Now().Year()
	}

	var htmlBuf, textBuf bytes.Buffer
	if err := r.html.ExecuteTemplate(&htmlBuf, "activation_link.html", d); err != nil {
		return Message{}, fmt.Errorf("email: render activation html: %w", err)
	}
	if err := r.text.ExecuteTemplate(&textBuf, "activation_link.txt", d); err != nil {
		return Message{}, fmt.Errorf("email: render activation text: %w", err)
	}
	return Message{
		Subject:  fmt.Sprintf("%s 激活账号", d.AppName),
		HTMLBody: htmlBuf.String(),
		TextBody: textBuf.String(),
	}, nil
}
