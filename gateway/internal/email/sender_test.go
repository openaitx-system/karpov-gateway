package email

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRenderer_VerificationCode(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	msg, err := r.RenderVerificationCode(VerificationCodeData{
		Code:           "123456",
		ExpiresMinutes: 10,
		AppName:        "TestApp",
		SubjectTitle:   "邮箱验证码",
		Greeting:       "你好，",
		SupportEmail:   "support@test.com",
		Year:           2026,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(msg.HTMLBody, "123456") {
		t.Errorf("html missing code: %q", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "123456") {
		t.Errorf("text missing code: %q", msg.TextBody)
	}
	if !strings.Contains(msg.Subject, "TestApp") {
		t.Errorf("subject missing app: %q", msg.Subject)
	}
	if !strings.Contains(msg.HTMLBody, "support@test.com") {
		t.Errorf("html missing support email")
	}
}

func TestRenderer_DefaultsApplied(t *testing.T) {
	r := MustNewRenderer()
	// 全部用零值；模板内 fallback 必须把 ExpiresMinutes / AppName 等都填上
	msg, err := r.RenderVerificationCode(VerificationCodeData{Code: "000000"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(msg.HTMLBody, "Karpov") {
		t.Errorf("default app name not applied")
	}
	if !strings.Contains(msg.HTMLBody, "10 分钟") {
		t.Errorf("default ExpiresMinutes not applied: %q", msg.HTMLBody)
	}
}

func TestMockSender_RecordsAndReplays(t *testing.T) {
	m := NewMockSender("from@test.com")
	if got := m.FromAddress(); got != "from@test.com" {
		t.Errorf("from = %q", got)
	}
	if err := m.Send(context.Background(), Message{To: "a@b.c", Subject: "s", TextBody: "t"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	last, ok := m.Last()
	if !ok || last.To != "a@b.c" {
		t.Errorf("last: %+v ok=%v", last, ok)
	}
	if len(m.Sent) != 1 {
		t.Errorf("sent count = %d", len(m.Sent))
	}

	// FailNext 注入错误
	m.FailNext = errors.New("smtp boom")
	if err := m.Send(context.Background(), Message{To: "x@y.z", Subject: "s", TextBody: "t"}); err == nil {
		t.Error("expected forced failure")
	}
	// 失败一次后自动清；下一封应正常
	if err := m.Send(context.Background(), Message{To: "x@y.z", Subject: "s", TextBody: "t"}); err != nil {
		t.Errorf("subsequent send failed unexpectedly: %v", err)
	}
}

func TestEncodeHeader_NonASCII(t *testing.T) {
	en := encodeHeader("Hello")
	if en != "Hello" {
		t.Errorf("ASCII should pass-through: %q", en)
	}
	zh := encodeHeader("【测试】邮箱验证码")
	if !strings.HasPrefix(zh, "=?UTF-8?B?") || !strings.HasSuffix(zh, "?=") {
		t.Errorf("non-ASCII not RFC 2047 encoded: %q", zh)
	}
}

func TestAssembleMIME_Multipart(t *testing.T) {
	body := assembleMIME("from@x.com", "From Name", Message{
		To:       "to@y.com",
		Subject:  "Test 中文",
		HTMLBody: "<p>html</p>",
		TextBody: "plain text",
	})
	for _, want := range []string{
		"From: ", "@x.com>", "To: to@y.com",
		"multipart/alternative; boundary=", "text/plain", "text/html",
		"plain text", "<p>html</p>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("MIME body missing %q", want)
		}
	}
}

func TestSMTPSender_ValidatesConfig(t *testing.T) {
	if _, err := NewSMTPSender(SMTPConfig{}); err == nil {
		t.Error("empty config should fail")
	}
	if _, err := NewSMTPSender(SMTPConfig{Host: "smtp.x", Port: 0, FromAddress: "f@x"}); err == nil {
		t.Error("bad port should fail")
	}
	if _, err := NewSMTPSender(SMTPConfig{Host: "smtp.x", Port: 587}); err == nil {
		t.Error("missing from should fail")
	}
}
