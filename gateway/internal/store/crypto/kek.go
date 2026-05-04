package crypto

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// LoadKEKFromEnv 从环境变量读 hex 编码的 KEK（64 个 hex 字符 → 32 字节）。
//
// 当 envName 未设置或解析失败时返回错误；调用方应在启动时一次性加载，
// 失败立即 fatal 而不是让运行期解密爆炸。
//
// 生产环境的 KEK 应由 KMS / Vault 派发；本函数仅作为本地开发与测试入口。
func LoadKEKFromEnv(envName string) ([]byte, error) {
	v := os.Getenv(envName)
	if v == "" {
		return nil, fmt.Errorf("crypto: env %q not set", envName)
	}
	return ParseKEKHex(v)
}

// ParseKEKHex 把 hex 字符串解码为 32 字节 KEK。
func ParseKEKHex(s string) ([]byte, error) {
	if len(s) != KeySize*2 {
		return nil, fmt.Errorf("crypto: KEK hex must be %d chars (got %d)", KeySize*2, len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("crypto: parse KEK hex: %w", err)
	}
	if len(b) != KeySize {
		return nil, errors.New("crypto: decoded KEK length != 32")
	}
	return b, nil
}
