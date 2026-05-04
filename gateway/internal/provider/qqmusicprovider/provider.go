// Package qqmusicprovider 把 qqmusic + modules 适配为 provider.MusicProvider。
//
// 单独成包是为了打破 qqmusic ↔ modules ↔ provider 的导入循环：
//   - qqmusic 提供底层 Client / Credential
//   - modules 调用 qqmusic.Client.RequestMusicu
//   - 本包 = 上层胶水，依赖 modules 与 provider，但**不被** qqmusic 导入。
package qqmusicprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/modules"
)

// providerName 是 QQ 音乐在 Registry 中的名字。
const providerName = "qqmusic"

// Provider 实现 provider.MusicProvider 接口。
//
// 持有一个无凭据的"骨架" Client（device/qimei/policy 等共享配置），每次调用时
// 通过 lease.Payload 反序列化 Credential 并临时挂到一个 per-call shadow client。
// 这样号池可以按 lease 粒度切换凭据，零锁竞争。
type Provider struct {
	base *qqmusic.Client // 共享配置：HTTP transport、device、qimei、policy、guid
}

// NewProvider 构造 QQ 音乐 provider。
//
// `base` 一般用 NewClient(ClientOptions{...}) 构造，凭据可留空（每次调用都会
// 用 lease.Payload 覆盖）；其它字段（HTTP、Device、Qimei、VersionPolicy、GUID）
// 在 base 上设置一次即可被所有 lease 共享。
func NewProvider(base *qqmusic.Client) *Provider {
	if base == nil {
		base = qqmusic.NewClient(qqmusic.ClientOptions{})
	}
	return &Provider{base: base}
}

// Name 实现 MusicProvider。
func (p *Provider) Name() string { return providerName }

// Capabilities 实现 MusicProvider；列出当前已实现的能力（M9 范围）。
func (p *Provider) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapGetSong,
		provider.CapSearchSongs,
		provider.CapGetSongURL,
		provider.CapGetLyric,
		provider.CapGetAlbum,
		provider.CapGetAlbumSongs,
		provider.CapGetSinger,
		provider.CapGetSingerSongs,
		provider.CapGetSongList,
		provider.CapGetMV,
		provider.CapGetMVURL,
		provider.CapGetTopList,
		provider.CapGetRecommend,
		provider.CapGetComments,
	}
}

// withLease 把 lease 中的 Credential 注入一个临时 Client（共享 base 的 transport/device）。
//
// 不修改 base：每次新建一个 Client value 拷贝（安全，因 Client 字段都是
// 引用语义或可拷贝的 immutable 配置）。
func (p *Provider) withLease(lease *provider.CredentialLease) (*qqmusic.Client, error) {
	cred := &qqmusic.Credential{}
	if lease != nil && len(lease.Payload) > 0 {
		if err := json.Unmarshal(lease.Payload, cred); err != nil {
			return nil, fmt.Errorf("qqmusic.provider: parse credential: %w", err)
		}
	}
	return p.base.WithCredential(cred), nil
}

// GetSong 实现 MusicProvider.GetSong：单首歌曲详情，按 mid。
func (p *Provider) GetSong(ctx context.Context, lease *provider.CredentialLease, mid string) (map[string]any, error) {
	c, err := p.withLease(lease)
	if err != nil {
		return nil, err
	}
	return modules.GetSongDetail(ctx, c, mid)
}

// SearchSongs 实现 MusicProvider.SearchSongs。
func (p *Provider) SearchSongs(ctx context.Context, lease *provider.CredentialLease, q string, page, size int) (map[string]any, error) {
	c, err := p.withLease(lease)
	if err != nil {
		return nil, err
	}
	resp, err := modules.SearchByType(ctx, c, q, modules.SearchByTypeOptions{
		Page: page, Num: size, Type: modules.SearchTypeSong,
		Highlight: true, // 与 Python search_by_type 默认 highlight=True 对齐
	})
	if err != nil {
		return nil, err
	}
	// 把 SearchResponse flatten 回 map[string]any 以保持接口一致
	out := map[string]any{
		"total":    resp.Total,
		"has_more": resp.HasMore,
		"list":     resp.List,
	}
	return out, nil
}

// GetSongURL 实现 MusicProvider.GetSongURL：quality 是 "M500"/"M800"/"F000" 等前缀。
func (p *Provider) GetSongURL(ctx context.Context, lease *provider.CredentialLease, mid string, quality string) (map[string]any, error) {
	c, err := p.withLease(lease)
	if err != nil {
		return nil, err
	}
	prefix, ext := splitQuality(quality)
	return modules.GetSongURLs(ctx, c, []string{mid}, prefix, ext, c.GUID())
}

// GetLyric 实现 MusicProvider.GetLyric。
func (p *Provider) GetLyric(ctx context.Context, lease *provider.CredentialLease, mid string) (map[string]any, error) {
	c, err := p.withLease(lease)
	if err != nil {
		return nil, err
	}
	return modules.GetLyric(ctx, c, mid, modules.GetLyricOptions{
		QRC:   true,
		Trans: true,
		Roma:  true,
	})
}

// HealthCheck 实现 MusicProvider.HealthCheck：调一次轻量 GetSong 探测。
//
// pool 健康检查 worker 会周期性调用这个方法；任何返回 error 都视为 lease 不健康。
func (p *Provider) HealthCheck(ctx context.Context, lease *provider.CredentialLease) error {
	c, err := p.withLease(lease)
	if err != nil {
		return err
	}
	// 用一个稳定存在的 mid 做探测；具体 mid 由 Pool Worker 配置（避免硬编码）
	// 这里使用 Test method "GetTopAll"——空参数、无凭据要求、流量小。
	_, err = modules.GetTopAll(ctx, c)
	return err
}

// GetAlbum 专辑详情。
func (p *Provider) GetAlbum(ctx context.Context, lease *provider.CredentialLease, mid string) (map[string]any, error) {
	c, err := p.withLease(lease)
	if err != nil {
		return nil, err
	}
	return modules.GetAlbumDetail(ctx, c, mid)
}

// GetSingerDetail 歌手详情.
//
// 走 `music.UnifiedHomepage.UnifiedHomepageSrv` / `GetHomepageHeader` (Python 的 `get_info`) ——
// 这是歌手主页 header, 任意普通号池号都能取到; 不再走 `SingerInfoInter` / `GetSingerDetail`
// (Python 的 `get_desc`) 因为后者常 104403.
func (p *Provider) GetSingerDetail(ctx context.Context, lease *provider.CredentialLease, mid string) (map[string]any, error) {
	c, err := p.withLease(lease)
	if err != nil {
		return nil, err
	}
	return modules.GetSingerInfo(ctx, c, mid)
}

// GetSongList 歌单详情。
func (p *Provider) GetSongList(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c, err := p.withLease(lease)
	if err != nil {
		return nil, err
	}
	// songlist id 是数字但 API 接受 int64
	var disstid int64
	for _, ch := range id {
		if ch >= '0' && ch <= '9' {
			disstid = disstid*10 + int64(ch-'0')
		}
	}
	if disstid == 0 {
		return nil, fmt.Errorf("invalid songlist id: %s", id)
	}
	return modules.GetSonglistDetail(ctx, c, disstid, 10, 1)
}

// TestCapability 用固定参数测试指定能力，返回 error=nil 表示通过。
func (p *Provider) TestCapability(ctx context.Context, lease *provider.CredentialLease, cap provider.Capability) error {
	c, err := p.withLease(lease)
	if err != nil {
		return err
	}
	const testMID = "001NgljR0RUhy1"

	switch cap {
	case provider.CapGetSong:
		_, err = modules.GetSongDetail(ctx, c, testMID)
	case provider.CapSearchSongs:
		_, err = modules.SearchByType(ctx, c, "周杰伦", modules.SearchByTypeOptions{Page: 1, Num: 1, Type: modules.SearchTypeSong})
	case provider.CapGetSongURL:
		_, err = modules.GetSongURLs(ctx, c, []string{testMID}, "M500", ".mp3", c.GUID())
	case provider.CapGetLyric:
		_, err = modules.GetLyric(ctx, c, testMID, modules.GetLyricOptions{})
	case provider.CapGetAlbum:
		_, err = modules.GetAlbumDetail(ctx, c, "002fRO0N4FftzY")
	case provider.CapGetAlbumSongs:
		_, err = modules.GetAlbumSongList(ctx, c, "002fRO0N4FftzY", 1, 1)
	case provider.CapGetSinger:
		_, err = modules.GetSingerDetail(ctx, c, []string{"0025NhlN2yWrP4"})
	case provider.CapGetSingerSongs:
		_, err = modules.GetSingerSongList(ctx, c, "0025NhlN2yWrP4", 1, 1)
	case provider.CapGetSongList:
		_, err = modules.GetSonglistDetail(ctx, c, 7272907725, 1, 1)
	case provider.CapGetTopList:
		_, err = modules.GetTopAll(ctx, c)
	case provider.CapGetRecommend:
		_, err = modules.GetRecommendHomepage(ctx, c)
	case provider.CapGetMV:
		_, err = modules.GetMVDetail(ctx, c, []string{"r00252in0dn"})
	case provider.CapGetMVURL:
		_, err = modules.GetMVURLs(ctx, c, []string{"r00252in0dn"})
	case provider.CapGetComments:
		_, err = modules.GetHotComments(ctx, c, modules.CommentBizSong, "102065756", 1, 1)
	default:
		_, err = modules.GetTopAll(ctx, c)
	}
	return err
}

func splitQuality(q string) (prefix, ext string) {
	switch q {
	// 臻品/母带/全景声
	case "AI00", "MASTER":
		return "AI00", ".flac"
	case "Q000", "ATMOS_2":
		return "Q000", ".flac"
	case "Q001", "ATMOS_51":
		return "Q001", ".flac"
	case "Q003", "ATMOS_71":
		return "Q003", ".ogg"
	case "D004", "ATMOS_DB", "DOLBY":
		return "D004", ".mp4"
	case "DT03", "DTS_X":
		return "DT03", ".mp4"
	case "TL01", "NAC":
		return "TL01", ".nac"
	// 无损
	case "F000", "FLAC", "LOSSLESS":
		return "F000", ".flac"
	// OGG
	case "O801", "OGG_640":
		return "O801", ".ogg"
	case "O800", "OGG_320":
		return "O800", ".ogg"
	case "O600", "OGG_192":
		return "O600", ".ogg"
	case "O400", "OGG_96":
		return "O400", ".ogg"
	// MP3
	case "M800", "MP3_320", "HIGH":
		return "M800", ".mp3"
	case "M500", "MP3_128", "STANDARD":
		return "M500", ".mp3"
	// AAC
	case "C600", "ACC_192", "AAC_192":
		return "C600", ".m4a"
	case "C400", "ACC_96", "AAC_96":
		return "C400", ".m4a"
	case "C200", "ACC_48", "AAC_48":
		return "C200", ".m4a"
	// 加密格式
	case "F0M0", "MFLAC":
		return "F0M0", ".mflac"
	case "O8M0", "MOGG_320":
		return "O8M0", ".mgg"
	case "O8M1", "MOGG_640":
		return "O8M1", ".mgg"
	case "AIM0", "MMASTER":
		return "AIM0", ".mflac"
	case "Q0M0", "MATMOS_2":
		return "Q0M0", ".mflac"
	default:
		return "M500", ".mp3"
	}
}

// Register 把 QQ 音乐 Provider 注册到给定 Registry。
//
// 调用方典型用法：在 cmd/music/main.go 启动时调一次。
func Register(reg *provider.Registry, base *qqmusic.Client) error {
	if reg == nil {
		return errors.New("qqmusic.Register: nil registry")
	}
	return reg.Register(NewProvider(base))
}
