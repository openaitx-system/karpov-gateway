package crypto

import (
	"context"
	"errors"
	"fmt"
)

// KeyProvider 抽象 KEK 来源。
//
// 实现：
//   - StaticKeyProvider：字节切片（测试 / 已通过其他途径加载）
//   - EnvKeyProvider：env 变量 hex
//   - （未来）KMSKeyProvider / VaultKeyProvider：v0.4 接入
//
// Provider 应当：
//   - 缓存 KEK 避免每次调用都查 KMS
//   - 在多 KEK 版本场景下用 keyID 派发（v0.4 多版本 KEK 落地时引入）
//
// v0.3 范围：单 KEK 版本，Get(ctx, "") 即拿到当前活跃 KEK。
type KeyProvider interface {
	// Get 返回当前活跃 KEK；keyID 为空表示默认。
	//
	// 失败应返回错误而不是退化到空 KEK，调用方在 boot 时一次性校验。
	Get(ctx context.Context, keyID string) ([]byte, error)
}

// ErrUnknownKeyID 表示请求了 provider 不持有的 keyID。
var ErrUnknownKeyID = errors.New("crypto: unknown key id")

// StaticKeyProvider 持有一份明文 KEK；测试或外部已加载场景使用。
type StaticKeyProvider struct {
	kek []byte
}

// NewStaticKeyProvider 构造 StaticKeyProvider；KEK 长度必须为 KeySize。
func NewStaticKeyProvider(kek []byte) (*StaticKeyProvider, error) {
	if len(kek) != KeySize {
		return nil, ErrInvalidKEK
	}
	cp := make([]byte, KeySize)
	copy(cp, kek)
	return &StaticKeyProvider{kek: cp}, nil
}

// Get 实现 KeyProvider.Get；忽略 keyID（v0.3 单 KEK 版本）。
func (p *StaticKeyProvider) Get(_ context.Context, _ string) ([]byte, error) {
	return p.kek, nil
}

// EnvKeyProvider 在 boot 时一次从 env 解析 hex KEK；运行期不再 IO。
type EnvKeyProvider struct {
	envName string
	kek     []byte
}

// NewEnvKeyProvider 从 env 加载 hex KEK；env 缺失或长度不对返回错误。
func NewEnvKeyProvider(envName string) (*EnvKeyProvider, error) {
	kek, err := LoadKEKFromEnv(envName)
	if err != nil {
		return nil, fmt.Errorf("crypto.NewEnvKeyProvider: %w", err)
	}
	return &EnvKeyProvider{envName: envName, kek: kek}, nil
}

// Get 实现 KeyProvider.Get。
func (p *EnvKeyProvider) Get(_ context.Context, _ string) ([]byte, error) {
	return p.kek, nil
}

// EnvName 返回 provider 加载用的 env 名（便于日志）。
func (p *EnvKeyProvider) EnvName() string { return p.envName }
