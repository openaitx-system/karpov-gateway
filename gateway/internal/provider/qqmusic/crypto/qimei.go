// Package crypto 实现 QIMEI 设备指纹申请所需的加密原语：
// RSA-PKCS1v15 公钥加密 + AES-CBC PKCS7 padding（IV=Key 这一非标准约定）。
//
// 1:1 对齐 qqmusic_api/utils/qimei.py。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// QimeiPublicKeyPEM 与 qqmusic_api/utils/qimei.py:26-28 PUBLIC_KEY 完全一致。
const QimeiPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDEIxgwoutfwoJxcGQeedgP7FG9qaIuS0qzfR8gWkrkTZKM2iWHn2ajQpBRZjMSoSf6+KJGvar2ORhBfpDXyVtZCKpqLQ+FLkpncClKVIrBwv6PHyUvuCb0rIarmgDnzkfQAqVufEtR64iazGDKatvJ9y6B9NMbHddGSAUmRTCrHQIDAQAB
-----END PUBLIC KEY-----`

// QimeiSecret / QimeiAppKey 与 qimei.py:29-30 一致；用于 calc_md5 链。
const (
	QimeiSecret = "ZdJqM15EeO2zWc08"
	QimeiAppKey = "0AND0HD6FE4HY80F"
)

// pubKey 解析一次 PEM；并发安全（只读）。
var pubKey *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(QimeiPublicKeyPEM))
	if block == nil {
		panic("qqmusic/crypto: PUBLIC_KEY PEM decode failed")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic(fmt.Sprintf("qqmusic/crypto: parse PUBLIC_KEY: %v", err))
	}
	rk, ok := pub.(*rsa.PublicKey)
	if !ok {
		panic(fmt.Sprintf("qqmusic/crypto: PUBLIC_KEY is %T, not *rsa.PublicKey", pub))
	}
	pubKey = rk
}

// RSAEncrypt 使用 QimeiPublicKey + PKCS#1v1.5 padding 加密。
//
// 注：每次输出含随机 padding，密文不可对拍；只能验证长度（=128 = 1024-bit 模长）
// 与 Go 自身的解密逆等性（用于自检）。
func RSAEncrypt(plaintext []byte) ([]byte, error) {
	return rsa.EncryptPKCS1v15(rand.Reader, pubKey, plaintext)
}

// AESEncryptCBC 用同一个 key 既作密钥又作 IV，PKCS7 填充明文后 AES-CBC 加密。
//
// 这是 qimei.py:67-70 的特殊约定（`Cipher(AES(key), CBC(key))`），
// IV=Key 在标准密码学场景里不安全，但协议要求一致；不要扩展到其他场景。
//
// key 长度须为 16 字节（AES-128）；其它长度返回错误。
func AESEncryptCBC(key, plaintext []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("qqmusic/crypto: AES key must be 16 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("qqmusic/crypto: new AES cipher: %w", err)
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	out := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, key) // IV = Key
	mode.CryptBlocks(out, padded)
	return out, nil
}

// pkcs7Pad 实现 PKCS7 padding，等价于 qimei.py:68:
//
//	padding_size = 16 - len(content) % 16
//	content + (padding_size * chr(padding_size)).encode()
//
// 即便 len%16==0 也补一整块（标准 PKCS7 行为，与 Python chr(16)*16 一致）。
func pkcs7Pad(in []byte, blockSize int) []byte {
	if blockSize <= 0 || blockSize > 0xFF {
		panic("qqmusic/crypto: pkcs7Pad bad block size")
	}
	pad := blockSize - len(in)%blockSize
	out := make([]byte, len(in)+pad)
	copy(out, in)
	for i := len(in); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// PKCS7Unpad 移除 PKCS7 padding；返回原文或 padding 错误。
// 主要用于 AES 解密自检测试。
func PKCS7Unpad(in []byte, blockSize int) ([]byte, error) {
	if len(in) == 0 || len(in)%blockSize != 0 {
		return nil, errors.New("qqmusic/crypto: PKCS7 unpad: bad length")
	}
	pad := int(in[len(in)-1])
	if pad == 0 || pad > blockSize {
		return nil, errors.New("qqmusic/crypto: PKCS7 unpad: bad padding")
	}
	for i := len(in) - pad; i < len(in); i++ {
		if int(in[i]) != pad {
			return nil, errors.New("qqmusic/crypto: PKCS7 unpad: corrupted padding")
		}
	}
	return in[:len(in)-pad], nil
}
