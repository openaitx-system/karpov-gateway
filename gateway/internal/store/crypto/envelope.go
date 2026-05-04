// Package crypto 提供凭据 envelope 加解密：AES-256-GCM + 随机 nonce。
//
// 设计目标（与 plan §6.4 / SECURITY.md 对齐）：
//
//   - KEK（Key Encryption Key，32 字节）来自 env / KMS / Vault；本包不持有
//     长期 KEK，仅在调用栈中传递。
//   - 每条 ciphertext = nonce(12) || sealed(plaintext + 16 字节 GCM tag)。
//   - AAD（Additional Authenticated Data，可选）用于把凭据 ID / Provider
//     绑定到密文，防止把凭据 A 的密文塞给 B 解密。
//
// 不在本包做的事：
//   - KEK 轮换 / 多版本管理（v0.2 用单 KEK；多版本由调用方按 prefix 派发）
//   - 字段级加密（业务用整 payload 即可）
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize 是 AES-256 的 key 长度（32 字节）。
const KeySize = 32

// nonceSize 是 GCM 标准 nonce 长度（12 字节）。每条密文都带新随机 nonce。
const nonceSize = 12

// ErrInvalidKEK 表示 KEK 长度不是 32 字节。
var ErrInvalidKEK = errors.New("crypto: KEK must be 32 bytes (AES-256)")

// ErrCipherTooShort 表示密文短于 nonce + tag，无法解密。
var ErrCipherTooShort = errors.New("crypto: ciphertext too short")

// Encrypt 用 KEK 加密 plaintext；可选 aad 参与 AEAD 校验。
//
// 输出格式：nonce(12) || ciphertext_with_tag。
// nonce 由 crypto/rand 产生，长度为 GCM 标准的 12 字节。
func Encrypt(kek, plaintext, aad []byte) ([]byte, error) {
	if len(kek) != KeySize {
		return nil, ErrInvalidKEK
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes new: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm new: %w", err)
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: rand nonce: %w", err)
	}
	// Seal 第一个参数是 dst（拼接 nonce 在前）；这样输出可以直接落盘。
	out := gcm.Seal(nonce, nonce, plaintext, aad)
	return out, nil
}

// Decrypt 用 KEK 解密 envelope；aad 必须与 Encrypt 时一致，否则 GCM 校验失败。
//
// 失败情形：
//   - KEK 长度不对：ErrInvalidKEK
//   - 密文短于 nonce+tag：ErrCipherTooShort
//   - GCM 校验失败（被篡改 / aad 不匹配 / 用错 KEK）：透传 cipher.AEAD 错误
func Decrypt(kek, envelope, aad []byte) ([]byte, error) {
	if len(kek) != KeySize {
		return nil, ErrInvalidKEK
	}
	if len(envelope) < nonceSize+16 { // 16 = GCM tag
		return nil, ErrCipherTooShort
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes new: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm new: %w", err)
	}
	nonce, ct := envelope[:nonceSize], envelope[nonceSize:]
	plain, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm open: %w", err)
	}
	return plain, nil
}

// GenerateKEK 产生一个加密强度的 32 字节 KEK；仅用于测试 / 引导脚本。
//
// 生产环境的 KEK 应由 KMS / Vault 派发，绝不写入代码或 .env 之外的位置。
func GenerateKEK() ([]byte, error) {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("crypto: rand kek: %w", err)
	}
	return k, nil
}
