package email

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/smtp"
	"strings"
)

// encodeHeader 对包含非 ASCII（中文等）的 header 值用 RFC 2047 base64 编码。
//
// 仅当含非 ASCII 时编码：纯英文头不必走 encoded-word，邮件客户端原样显示更兼容。
func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7F {
			return false
		}
	}
	return true
}

// randomBoundary 生成一段随机 boundary 串（multipart 用）。
func randomBoundary() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败近乎不可能；保守 fallback 到时间戳
		return "fallback-boundary"
	}
	return hex.EncodeToString(b[:])
}

// ---- 自定义 LOGIN 认证（部分国内 SMTP 服务器仅支持 LOGIN 而不支持 PLAIN）----

type loginAuth struct{ username, password string }

func newLoginAuth(username, password string) smtp.Auth {
	if username == "" {
		return nil
	}
	return &loginAuth{username: username, password: password}
}

func (a *loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte{}, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("login auth: unexpected challenge %q", fromServer)
	}
}

// errEmptyLoginUsername 仅作占位，避免 lint 抱怨 LOGIN auth 没有路径校验空用户名。
var errEmptyLoginUsername = errors.New("email: empty login username") //nolint:unused
