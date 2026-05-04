// Package provider 是多音乐平台 Provider 抽象层。
//
// 上游业务（Music Service）按 (Provider, Capability) 调度，先向 pool.Acquire
// 取一个 CredentialLease，然后调用对应 MusicProvider 方法，最后 lease.Release。
// 失败回退最多 N 次换凭据（具体策略由 Music Service 拥有，Provider 只透传错误）。
package provider

import (
	"context"
	"errors"
)

// Capability 标识 Provider 能提供的能力。
//
// 使用整型枚举而非 string 以便 fast lookup（号池调度热路径上要按 capability
// 过滤候选凭据）。命名时**不**绑定具体 module/method 名（避免 Python→Go 映射后变更）。
type Capability int

const (
	CapNone Capability = iota
	CapGetSong
	CapSearchSongs
	CapGetSongURL
	CapGetLyric
	CapGetAlbum
	CapGetAlbumSongs
	CapGetSinger
	CapGetSingerSongs
	CapGetSongList
	CapGetMV
	CapGetMVURL
	CapGetTopList
	CapGetUser
	CapGetRecommend
	CapGetComments
	CapLoginQR
	CapLoginPhone
)

// PoolResult 是凭据使用结果，用于 Release 时调整健康度。
type PoolResult int

const (
	PoolResultOK PoolResult = iota
	PoolResultRateLimited
	PoolResultAuthFailed
	PoolResultNetworkError
)

// CredentialLease 是从 Pool.Acquire 取到的一次性凭据租约。
//
// 字段都是只读快照；Payload 为 provider 特定的反序列化原料（QQ 音乐就是 JSON
// Credential），由 Provider 自己解码。Release 必须被调用一次（建议 defer）。
type CredentialLease struct {
	ID       string
	Provider string
	Payload  []byte
	Release  func(PoolResult)
}

// MusicProvider 是各平台必须实现的接口。
//
// 设计原则：每个方法签名固定为 (ctx, lease, args...) → (result, error)。
// 这样号池调度可以按方法粒度切换 lease，而不是 client 粒度。lease.Release
// 由 Music Service 在 defer 里负责，Provider 内部不做。
//
// 这里定义最常见的能力子集；M9 之外的能力（Album/Singer/Songlist/...）按
// 业务需要逐步加方法。每个方法返回 map[string]any（raw data）以避免 provider
// 之间的 schema 强耦合，由 Music Service 适配为 domain.* 模型。
type MusicProvider interface {
	Name() string
	Capabilities() []Capability

	GetSong(ctx context.Context, lease *CredentialLease, mid string) (map[string]any, error)
	SearchSongs(ctx context.Context, lease *CredentialLease, q string, page, size int) (map[string]any, error)
	GetSongURL(ctx context.Context, lease *CredentialLease, mid string, quality string) (map[string]any, error)
	GetLyric(ctx context.Context, lease *CredentialLease, mid string) (map[string]any, error)

	HealthCheck(ctx context.Context, lease *CredentialLease) error
}

// ErrUnsupportedCapability 表示 provider 未实现该 capability（应在 Music
// Service 路由时被提前过滤，仅作 defensive 用）。
var ErrUnsupportedCapability = errors.New("provider: unsupported capability")
