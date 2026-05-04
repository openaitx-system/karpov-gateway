package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FlowState 是一次 OAuth 跳转的全部上下文, 序列化后塞进 cookie.
//
// 字段说明:
//   - Provider: 目标 provider 名 (跨 provider 防混用; cookie 名也按 provider 区分);
//   - State: 32 字节随机, urlsafe base64 编码; 同时是给 provider 的 state= 参数;
//   - PKCEVerifier: 43+ 字符的 RFC 7636 verifier; provider 端只见 challenge;
//   - Intent: "login" (匿名登录) / "bind" (登录后绑定) / "register" (跟登录同义, 留作扩展);
//   - NextURL: 成功后前端跳转地址 (white-list 验证, 防 open redirect);
//   - UserID: intent=bind 时本地账号 ID (callback 校验当前会话是否还是同一人);
//   - IssuedAt: 签发时刻; 超过 15 min 无效.
type FlowState struct {
	Provider     string `json:"p"`
	State        string `json:"s"`
	PKCEVerifier string `json:"v"`
	Intent       string `json:"i"`
	NextURL      string `json:"n,omitempty"`
	UserID       string `json:"u,omitempty"`
	IssuedAt     int64  `json:"t"`
}

// FlowStateMaxAge 是 cookie 寿命 (15 min). 超过即视为过期 (防长期保留泄露).
const FlowStateMaxAge = 15 * time.Minute

// FlowStateCookiePrefix 是 cookie 名前缀, 完整 cookie 名: oauth_state_<provider>.
//
// 按 provider 拆开 cookie 防止: 用户在 tab A 起 linuxdo flow, tab B 起 github flow,
// 后者覆盖前者 cookie 导致 A 回调 state 丢失.
const FlowStateCookiePrefix = "oauth_state_"

// CookieName 返回某 provider 的 state cookie 名.
func CookieName(provider string) string {
	return FlowStateCookiePrefix + provider
}

// FlowStateCodec 负责 FlowState 的 sign+seal: HMAC-SHA256(secret) 后 base64url.
//
// 不用 AEAD 是因为 cookie 内容不需要保密 (provider 端拿不到 cookie),
// 只需要防 tamper. HMAC 足够, 也避免 KEK 跟普通密码绑定.
type FlowStateCodec struct {
	secret []byte // HMAC 用; 32 字节; 由 caller 提供 (复用 KEK 或单独配)
}

// NewFlowStateCodec 构造 codec; secret 长度必须 >= 16 字节.
func NewFlowStateCodec(secret []byte) (*FlowStateCodec, error) {
	if len(secret) < 16 {
		return nil, errors.New("oauth: flow-state secret must be >= 16 bytes")
	}
	return &FlowStateCodec{secret: secret}, nil
}

// Encode 把 state 序列化为 cookie value 字符串: base64url(payload) + "." + base64url(mac).
func (c *FlowStateCodec) Encode(s *FlowState) (string, error) {
	if s.IssuedAt == 0 {
		s.IssuedAt = time.Now().Unix()
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("oauth: marshal flow-state: %w", err)
	}
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(payload)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// Decode 校验 + 解码; 失败 (篡改 / 过期 / 格式错) 返回 error.
func (c *FlowStateCodec) Decode(v string) (*FlowState, error) {
	if v == "" {
		return nil, errors.New("oauth: empty cookie")
	}
	parts := strings.SplitN(v, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("oauth: malformed cookie")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("oauth: cookie payload decode: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("oauth: cookie sig decode: %w", err)
	}
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("oauth: cookie signature mismatch")
	}
	var s FlowState
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, fmt.Errorf("oauth: cookie unmarshal: %w", err)
	}
	if time.Since(time.Unix(s.IssuedAt, 0)) > FlowStateMaxAge {
		return nil, errors.New("oauth: flow state expired")
	}
	return &s, nil
}

// ---- 一次性随机 ----

// NewRandomToken 生成 nByte 字节的 url-safe 随机串 (state / PKCE verifier 用).
func NewRandomToken(nBytes int) (string, error) {
	if nBytes <= 0 {
		nBytes = 32
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PKCEChallengeS256 把 verifier 转成 S256 challenge (base64url(SHA256(verifier))).
func PKCEChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
