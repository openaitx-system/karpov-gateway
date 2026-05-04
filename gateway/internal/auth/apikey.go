package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// APIKey 是 API Key 完整字符串：mk_<24B base64url>。
//
// 总长 = "mk_" (3) + base64url(24B raw → 32 字符) = 35 字符。
// 仅在创建时返回明文一次；DB 仅存 argon2id(明文) 与 8 字符 prefix。
type APIKey struct {
	Plain  string // 明文，仅创建时返回
	Prefix string // 前 8 字符（明文存 DB，便于 list / debug）
	Hash   string // argon2id 哈希（DB 持久化）
}

// NewAPIKey 生成一个随机 API Key 并完成 hash。
//
// 与 Plan §8.2 表中 prefix 8 字符 + hash 落库行为对齐。
func NewAPIKey(p PasswordParams) (*APIKey, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("auth.NewAPIKey: %w", err)
	}
	plain := "mk_" + base64.RawURLEncoding.EncodeToString(raw[:])
	prefix := plain[:8] // "mk_xxxxx"
	hash, err := HashPassword(plain, p)
	if err != nil {
		return nil, err
	}
	return &APIKey{Plain: plain, Prefix: prefix, Hash: hash}, nil
}

// VerifyAPIKey 用同样的 argon2id 校验明文 key 与 hash。
//
// 与 VerifyPassword 复用同一实现：argon2 自带恒定时间，subtle.ConstantTimeCompare
// 比较 hash 字节，避免 hash 字节被 plaintext 探测。
func VerifyAPIKey(plain, hash string) (bool, error) {
	if len(plain) < 4 || plain[:3] != "mk_" {
		return false, ErrAPIKeyMalformed
	}
	return VerifyPassword(plain, hash)
}

// ErrAPIKeyMalformed 表示传入的 API Key 格式不符合 mk_<...> 规范。
var ErrAPIKeyMalformed = errors.New("auth: api key malformed (missing 'mk_' prefix)")
