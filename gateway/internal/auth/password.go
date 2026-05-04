// Package auth 实现用户身份系统：密码 / Session / API Key / TOTP。
//
// 设计规范（Plan §8.2）：
//   - 密码哈希用 argon2id（OWASP #1 推荐）
//   - 不用 JWT，登录态走服务端 Session（Redis）+ HttpOnly Cookie
//   - 时序攻击：password verify 与 API key verify 都用 constant-time 比较
//   - Session 固化：登录成功必须重生成 SID，旧 SID 立即失效
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordParams 是 argon2id 的参数（OWASP 2024 推荐：t=2, m=64MiB, p=1）。
//
// 输出格式：$argon2id$v=19$m=65536,t=2,p=1$<salt-b64>$<hash-b64>
type PasswordParams struct {
	TimeCost   uint32 // 迭代次数
	MemoryCost uint32 // KiB
	Threads    uint8
	SaltLen    uint32
	KeyLen     uint32
}

// DefaultPasswordParams 是生产默认。
func DefaultPasswordParams() PasswordParams {
	return PasswordParams{
		TimeCost:   2,
		MemoryCost: 64 * 1024,
		Threads:    1,
		SaltLen:    16,
		KeyLen:     32,
	}
}

// HashPassword 用 argon2id 生成密码哈希。
//
// 输出已经包含算法标识、参数与盐，可直接存 PG 字段；后续 Verify 不需要单独存盐。
func HashPassword(plain string, p PasswordParams) (string, error) {
	if plain == "" {
		return "", errors.New("auth: empty password")
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth.HashPassword: salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, p.TimeCost, p.MemoryCost, p.Threads, p.KeyLen)
	return formatArgon2id(p, salt, key), nil
}

// VerifyPassword 校验明文密码与哈希；任何解析或比较失败都返回 false（恒定时间）。
func VerifyPassword(plain, encoded string) (bool, error) {
	p, salt, key, err := parseArgon2id(encoded)
	if err != nil {
		return false, err
	}
	want := argon2.IDKey([]byte(plain), salt, p.TimeCost, p.MemoryCost, p.Threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(key, want) == 1, nil
}

// formatArgon2id 输出 PHC 格式字符串。
func formatArgon2id(p PasswordParams, salt, key []byte) string {
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryCost, p.TimeCost, p.Threads,
		enc.EncodeToString(salt), enc.EncodeToString(key))
}

// parseArgon2id 解析 PHC 字符串。
func parseArgon2id(s string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 {
		return PasswordParams{}, nil, nil, errors.New("auth: invalid argon2id format")
	}
	if parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, errors.New("auth: not argon2id")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return PasswordParams{}, nil, nil, err
	}
	if version != argon2.Version {
		return PasswordParams{}, nil, nil, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}
	var p PasswordParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryCost, &p.TimeCost, &p.Threads); err != nil {
		return PasswordParams{}, nil, nil, err
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}
	key, err := enc.DecodeString(parts[5])
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(key))
	return p, salt, key, nil
}
