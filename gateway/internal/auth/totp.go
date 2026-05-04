package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
)

// TOTPGenerate 生成新 TOTP 凭据；返回 secret 与 otpauth:// URL（用于二维码）。
//
// 按 RFC 6238：30s 周期、SHA1（Authenticator 兼容性最佳）、6 位 code。
func TOTPGenerate(issuer, accountName string) (secret, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      30,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// TOTPVerify 校验 6 位 code 是否在 ±1 周期内有效（容忍时钟漂移 ±30s）。
//
// 严格 RFC 6238 不允许漂移；这里±1 是 Google Authenticator / Authy 的实践默认。
func TOTPVerify(secret, code string) bool {
	valid, err := totp.ValidateCustom(code, secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return false
	}
	return valid
}

// TOTPReplayBlocker 防止 30s 窗口内同一 code 被重放。
//
// 实现：把 (user_id, code) 存 Redis 30s 黑名单；任何已使用 code 不可二次通过。
type TOTPReplayBlocker struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewTOTPReplayBlocker 构造 replay blocker。
func NewTOTPReplayBlocker(rdb *redis.Client, keyPrefix string) *TOTPReplayBlocker {
	if keyPrefix == "" {
		keyPrefix = "auth:totp:used"
	}
	return &TOTPReplayBlocker{rdb: rdb, keyPrefix: keyPrefix}
}

// CheckAndMark 检查 code 是否已用过；未用过则原子性标记并返回 true。
//
// 用 Redis SETNX + EXPIRE 实现：仅一个调用方能 SETNX 成功。
func (b *TOTPReplayBlocker) CheckAndMark(ctx context.Context, userID, code string) (bool, error) {
	if userID == "" || code == "" {
		return false, errors.New("auth.TOTPReplayBlocker: empty user/code")
	}
	key := fmt.Sprintf("%s:%s:%s", b.keyPrefix, userID, code)
	ok, err := b.rdb.SetNX(ctx, key, "1", 30*time.Second).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}
