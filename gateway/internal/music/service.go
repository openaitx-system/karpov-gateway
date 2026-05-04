package music

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// Service 是 Music Service 的入口。
//
// 持有 provider Registry 与 Pool；其它 cross-cutting（Quota / Tracing / Logging）
// 由 Edge Gateway middleware 处理，本服务专注业务编排。
type Service struct {
	reg    *provider.Registry
	pool   PoolAcquirer
	maxTry int // 失败回退次数上限（默认 3）
}

// PoolAcquirer 抽出 *pool.Service 中实际用到的方法，便于测试 mock。
type PoolAcquirer interface {
	Acquire(ctx context.Context, providerName string, opts pool.AcquireOptions) (*provider.CredentialLease, error)
}

// NewService 构造 Music Service。
func NewService(reg *provider.Registry, p PoolAcquirer, maxTry int) *Service {
	if maxTry <= 0 {
		maxTry = 3
	}
	return &Service{reg: reg, pool: p, maxTry: maxTry}
}

// 错误类型对外不暴露太多细节；上层根据 errors.Is 判断分类即可。
var (
	ErrUnknownProvider      = errors.New("music: unknown provider")
	ErrAllCredentialsFailed = errors.New("music: all credentials exhausted")
	// ErrDataError 表示业务永久性错误（参数非法、资源不存在等）。
	// Provider 实现把这类错误 wrap 后抛出，Service 不会重试换凭据。
	ErrDataError = errors.New("music: data error")
)

// callWithLease 通用模板：
//  1. registry.Get(providerName)
//  2. for i in maxTry: pool.Acquire → fn(provider, lease) → 根据错误类型 release & 决定是否重试
//
// fn 拿到 (provider, lease) 后调用 provider 方法返回 (data, error)。
func (s *Service) callWithLease(ctx context.Context, providerName string, cap provider.Capability, fn func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error)) (any, error) {
	prov, ok := s.reg.Get(providerName)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, providerName)
	}
	var lastErr error
	for try := 0; try < s.maxTry; try++ {
		lease, err := s.pool.Acquire(ctx, providerName, pool.AcquireOptions{Capability: cap})
		if err != nil {
			lastErr = err
			// pool 不可用则直接退出，没有意义再 retry
			break
		}
		out, err := fn(prov, lease)
		if err == nil {
			lease.Release(provider.PoolResultOK)
			return out, nil
		}
		// 失败分类。当前默认 = NetworkError 触发回退；显式标记的"业务数据错误"
		// 不重试。M16 时把 provider 各家具体错误码映射到 PoolResult。
		result := classify(err)
		lease.Release(result)
		lastErr = err
		// 仅 ErrDataError（永久性业务错误）阻止 retry；其它都重试。
		if errors.Is(err, ErrDataError) {
			return nil, err
		}
		_ = result
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrAllCredentialsFailed, lastErr)
	}
	return nil, ErrAllCredentialsFailed
}

// classify 把 error 映射到 PoolResult。
//
// 当前简化：除非显式 ErrDataError，统一视为 NetworkError 触发回退并降健康分。
// M16 时按 provider 错误细化 RateLimited / AuthFailed。
func classify(err error) provider.PoolResult {
	if errors.Is(err, ErrDataError) {
		return provider.PoolResultOK
	}
	return provider.PoolResultNetworkError
}

// GetSong 实现：取 lease → provider.GetSong → 适配为 Song。
func (s *Service) GetSong(ctx context.Context, providerName, mid string) (*Song, error) {
	out, err := s.callWithLease(ctx, providerName, provider.CapGetSong, func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
		return p.GetSong(ctx, lease, mid)
	})
	if err != nil {
		return nil, err
	}
	raw := out.(map[string]any)
	return adaptSong(providerName, raw), nil
}

// SearchSongs 实现：分页搜索。
func (s *Service) SearchSongs(ctx context.Context, providerName, query string, page Page) (*SearchResult, error) {
	if page.Size <= 0 {
		page.Size = 10
	}
	if page.Page <= 0 {
		page.Page = 1
	}
	out, err := s.callWithLease(ctx, providerName, provider.CapSearchSongs, func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
		return p.SearchSongs(ctx, lease, query, page.Page, page.Size)
	})
	if err != nil {
		return nil, err
	}
	raw := out.(map[string]any)
	return adaptSearch(providerName, raw), nil
}

// GetSongURL 实现：取 lease → provider.GetSongURL → 提取 url。
func (s *Service) GetSongURL(ctx context.Context, providerName, mid, quality string) (*SongURL, error) {
	out, err := s.callWithLease(ctx, providerName, provider.CapGetSongURL, func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
		return p.GetSongURL(ctx, lease, mid, quality)
	})
	if err != nil {
		return nil, err
	}
	raw := out.(map[string]any)
	switch providerName {
	case "netease":
		return adaptSongURLNetease(quality, raw), nil
	default:
		return adaptSongURL(quality, raw), nil
	}
}

// GetLyric 实现：取歌词。
func (s *Service) GetLyric(ctx context.Context, providerName, mid string) (map[string]any, error) {
	out, err := s.callWithLease(ctx, providerName, provider.CapGetLyric, func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
		return p.GetLyric(ctx, lease, mid)
	})
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

// GetArtist 取歌手主页基础信息, 适配为 Artist domain.
//
// QQ 音乐走 `GetHomepageHeader`; 网易云目前没接, 暂返回 nil + ErrUnknownProvider.
func (s *Service) GetArtist(ctx context.Context, providerName, mid string) (*Artist, error) {
	out, err := s.callWithLease(ctx, providerName, provider.CapGetSinger, func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
		return p.(interface {
			GetSingerDetail(context.Context, *provider.CredentialLease, string) (map[string]any, error)
		}).GetSingerDetail(ctx, lease, mid)
	})
	if err != nil {
		return nil, err
	}
	raw := out.(map[string]any)
	return adaptArtist(providerName, raw), nil
}

// GetPlaylist 取歌单详情, 适配为 Playlist domain (含曲目).
func (s *Service) GetPlaylist(ctx context.Context, providerName, id string) (*Playlist, error) {
	out, err := s.callWithLease(ctx, providerName, provider.CapGetSongList, func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
		return p.(interface {
			GetSongList(context.Context, *provider.CredentialLease, string) (map[string]any, error)
		}).GetSongList(ctx, lease, id)
	})
	if err != nil {
		return nil, err
	}
	raw := out.(map[string]any)
	return adaptPlaylist(providerName, raw), nil
}

// GetRawWithCap 通用原始数据调用：按 capability 调 provider 并返回 raw map。
// 用于 GetAlbum/GetArtist/GetPlaylist 等不需要 domain 适配的端点。
func (s *Service) GetRawWithCap(ctx context.Context, providerName string, cap provider.Capability, fn func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error)) (map[string]any, error) {
	out, err := s.callWithLease(ctx, providerName, cap, fn)
	if err != nil {
		return nil, err
	}
	raw, ok := out.(map[string]any)
	if !ok {
		return nil, ErrDataError
	}
	return raw, nil
}

// adaptSong 把 provider raw map 转为 domain.Song；当前仅取最常见字段，
// 不做复杂 jsonpath，留待 M16 时按需补强。
func adaptSong(providerName string, raw map[string]any) *Song {
	switch providerName {
	case "netease":
		return adaptSongNetease(raw)
	default:
		return adaptSongQQMusic(providerName, raw)
	}
}

func adaptSongQQMusic(providerName string, raw map[string]any) *Song {
	out := &Song{ProviderName: providerName, Raw: raw}
	src := raw
	if ti, ok := raw["track_info"].(map[string]any); ok {
		src = ti
	}
	out.MID = strFromMap(src, "mid")
	out.Name = strFromMap(src, "name")
	if out.Name == "" {
		out.Name = strFromMap(src, "title")
	}
	out.Duration = intFromMap(src, "interval")

	if v, ok := src["pay"].(map[string]any); ok {
		out.VipOnly = intFromMap(v, "pay_play") == 1
		out.Playable = intFromMap(v, "pay_play") == 0
	} else {
		out.Playable = true
	}

	out.PublishDate = strFromMap(src, "time_public")

	if singer, ok := src["singer"].([]any); ok {
		for _, s := range singer {
			if m, ok := s.(map[string]any); ok {
				out.Singers = append(out.Singers, SongArtist{
					MID:  strFromMap(m, "mid"),
					Name: strFromMap(m, "name"),
				})
			}
		}
	}

	if album, ok := src["album"].(map[string]any); ok {
		out.Album = SongAlbum{
			MID:   strFromMap(album, "mid"),
			Name:  strFromMap(album, "name"),
			Cover: albumCoverURL(strFromMap(album, "pmid")),
		}
	}
	return out
}

func adaptSongNetease(raw map[string]any) *Song {
	out := &Song{ProviderName: "netease", Raw: raw}
	out.MID = numToStr(raw, "id")
	out.Name = strFromMap(raw, "name")
	// dt 是毫秒
	if dt, ok := raw["dt"].(float64); ok {
		out.Duration = int(dt / 1000)
	}
	// fee: 1=VIP
	fee := intFromMap(raw, "fee")
	out.VipOnly = fee == 1
	out.Playable = fee != 1

	// publishTime 是毫秒时间戳
	if pt, ok := raw["publishTime"].(float64); ok && pt > 0 {
		out.PublishDate = time.UnixMilli(int64(pt)).Format("2006-01-02")
	}

	// ar: [{id, name}, ...]
	if ar, ok := raw["ar"].([]any); ok {
		for _, a := range ar {
			if m, ok := a.(map[string]any); ok {
				out.Singers = append(out.Singers, SongArtist{
					MID:  numToStr(m, "id"),
					Name: strFromMap(m, "name"),
				})
			}
		}
	}

	// al: {id, name, picUrl}
	if al, ok := raw["al"].(map[string]any); ok {
		out.Album = SongAlbum{
			MID:   numToStr(al, "id"),
			Name:  strFromMap(al, "name"),
			Cover: strFromMap(al, "picUrl"),
		}
	}
	return out
}

func numToStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch n := v.(type) {
	case float64:
		return fmt.Sprintf("%.0f", n)
	case string:
		return n
	default:
		return fmt.Sprintf("%v", n)
	}
}

func strFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func intFromMap(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func albumCoverURL(pmid string) string {
	if pmid == "" {
		return ""
	}
	return "https://y.qq.com/music/photo_new/T002R300x300M000" + pmid + ".jpg"
}

// adaptSearch 适配搜索结果。
func adaptSearch(providerName string, raw map[string]any) *SearchResult {
	switch providerName {
	case "netease":
		return adaptSearchNetease(raw)
	default:
		return adaptSearchQQMusic(providerName, raw)
	}
}

func adaptSearchQQMusic(providerName string, raw map[string]any) *SearchResult {
	out := &SearchResult{}
	if v, ok := raw["total"].(int64); ok {
		out.Total = v
	} else if v, ok := raw["total"].(float64); ok {
		out.Total = int64(v)
	}
	if v, ok := raw["has_more"].(bool); ok {
		out.HasMore = v
	}
	if list, ok := raw["list"].([]map[string]any); ok {
		for _, m := range list {
			out.Items = append(out.Items, *adaptSong(providerName, m))
		}
	}
	return out
}

func adaptSearchNetease(raw map[string]any) *SearchResult {
	out := &SearchResult{}
	// 网易云搜索结果在 result.songs / result.songCount
	result := raw
	if r, ok := raw["result"].(map[string]any); ok {
		result = r
	}
	if v, ok := result["songCount"].(float64); ok {
		out.Total = int64(v)
	}
	out.HasMore = out.Total > 0
	if songs, ok := result["songs"].([]any); ok {
		for _, s := range songs {
			if m, ok := s.(map[string]any); ok {
				out.Items = append(out.Items, *adaptSongNetease(m))
			}
		}
	}
	return out
}

// adaptSongURL 提取播放链接。
// QQ 音乐结构：sip[] = CDN 域名列表，midurlinfo[].purl = 相对路径，完整 URL = sip[0]+purl。
func adaptSongURL(quality string, raw map[string]any) *SongURL {
	out := &SongURL{Quality: quality}

	// 取 CDN 域名：优先选非 dl.stream 的快速节点
	cdnBase := ""
	if sip, ok := raw["sip"].([]any); ok && len(sip) > 0 {
		for _, s := range sip {
			if u, ok := s.(string); ok && u != "" {
				if cdnBase == "" {
					cdnBase = u
				}
				// 优先 ws/isure 等快速节点
				if !strings.Contains(u, "dl.stream") {
					cdnBase = u
					break
				}
			}
		}
	}
	if cdnBase == "" {
		cdnBase = "https://ws.stream.qqmusic.qq.com/"
	}

	if mu, ok := raw["midurlinfo"].([]any); ok && len(mu) > 0 {
		if first, ok := mu[0].(map[string]any); ok {
			purl := strFromMap(first, "purl")
			if purl != "" {
				out.URL = cdnBase + purl
			}
			out.MID = strFromMap(first, "songmid")
			out.Subcode = intFromMap(first, "subcode")
			for _, key := range []string{"wififilesize", "filesize", "hisizeflac", "hisizeape", "size128", "size320", "sizeflac", "sizeape", "sizeogg"} {
				if sz, ok := first[key].(float64); ok && sz > 0 {
					out.Size = int64(sz)
					break
				}
			}
		}
	}

	// 备选
	if out.URL == "" {
		if u, ok := raw["url"].(string); ok {
			out.URL = u
		}
	}

	// 过期时间
	if e, ok := raw["expiration"].(float64); ok {
		out.Expires = int64(e)
	} else if e, ok := raw["expire"].(float64); ok {
		out.Expires = int64(e)
	}

	return out
}

// adaptArtist 把 provider raw map 转为 domain.Artist.
//
// 当前仅 QQ 音乐 (走 GetHomepageHeader); 网易云端待 M16 接通后再补.
func adaptArtist(providerName string, raw map[string]any) *Artist {
	switch providerName {
	case "netease":
		return adaptArtistNetease(raw)
	default:
		return adaptArtistQQMusic(providerName, raw)
	}
}

// adaptArtistQQMusic 抽取 GetHomepageHeader 响应:
//   - $.Info.Singer.{SingerMid, Name, SingerType, SingerPic, SingerPMid}
//   - $.Info.BaseInfo.{Name, Avatar, BackgroundImage}
//
// 缺字段时按优先级回填: name 先 Singer.Name 再 BaseInfo.Name; avatar 先 SingerPic 再 BaseInfo.Avatar.
func adaptArtistQQMusic(providerName string, raw map[string]any) *Artist {
	out := &Artist{ProviderName: providerName, Raw: raw}
	info, _ := raw["Info"].(map[string]any)
	if info == nil {
		// 兜底: 老协议把字段平铺在 raw 顶层
		info = raw
	}
	if singer, ok := info["Singer"].(map[string]any); ok {
		out.MID = strFromMap(singer, "SingerMid")
		out.Name = strFromMap(singer, "Name")
		out.Type = intFromMap(singer, "SingerType")
		out.Avatar = strFromMap(singer, "SingerPic")
		if out.Avatar == "" {
			// pmid 兜底拼 cover
			pmid := strFromMap(singer, "SingerPMid")
			if pmid != "" {
				out.Avatar = "https://y.gtimg.cn/music/photo_new/T001R300x300M000" + pmid + ".jpg"
			}
		}
	}
	if base, ok := info["BaseInfo"].(map[string]any); ok {
		if out.Name == "" {
			out.Name = strFromMap(base, "Name")
		}
		if out.Avatar == "" {
			out.Avatar = strFromMap(base, "Avatar")
		}
		out.Background = strFromMap(base, "BackgroundImage")
	}
	return out
}

// adaptArtistNetease 兼容两种网易云歌手响应:
//   - `/api/artist/head/info/get` (现 GetSingerDetail 默认) → `{data: {artist: {id, name, cover, briefDesc, transName, alias, ...}}}`
//   - `/api/v1/artist/{id}` (旧路径) → `{artist: {id, name, picUrl, briefDesc, ...}, hotSongs: [...]}`
//
// 字段优先级: 头像 = cover > picUrl > img1v1Url; 背景图 = cover (head/info/get 同一字段);
// briefDesc 走 ext (后续 handler 可选透出).
func adaptArtistNetease(raw map[string]any) *Artist {
	out := &Artist{ProviderName: "netease", Raw: raw}
	var src map[string]any
	// data.artist (head/info/get) 优先
	if d, ok := raw["data"].(map[string]any); ok {
		if a, ok := d["artist"].(map[string]any); ok {
			src = a
		}
	}
	// 兼容 raw.artist (旧 v1/artist 路径)
	if src == nil {
		if a, ok := raw["artist"].(map[string]any); ok {
			src = a
		}
	}
	if src == nil {
		src = raw
	}
	out.MID = numToStr(src, "id")
	out.Name = strFromMap(src, "name")
	if out.Name == "" {
		// 偶尔 head/info/get 把名字放 transName
		out.Name = strFromMap(src, "transName")
	}
	// head/info/get 返回的是 cover (高清), v1/artist 返回 picUrl
	out.Avatar = strFromMap(src, "cover")
	if out.Avatar == "" {
		out.Avatar = strFromMap(src, "picUrl")
	}
	if out.Avatar == "" {
		out.Avatar = strFromMap(src, "img1v1Url")
	}
	// 网易云 head/info/get 没有独立背景图字段, 复用 cover
	out.Background = strFromMap(src, "cover")
	return out
}

// adaptPlaylist 把 provider raw map 转为 domain.Playlist.
func adaptPlaylist(providerName string, raw map[string]any) *Playlist {
	switch providerName {
	case "netease":
		return adaptPlaylistNetease(raw)
	default:
		return adaptPlaylistQQMusic(providerName, raw)
	}
}

// adaptPlaylistQQMusic 抽取 CgiGetDiss 响应:
//   - $.dirinfo (基础元 + creator)
//   - $.songlist[*] (本页曲目, 直接 reuse adaptSongQQMusic)
//   - $.{total_song_num, songlist_size, hasmore}
func adaptPlaylistQQMusic(providerName string, raw map[string]any) *Playlist {
	out := &Playlist{ProviderName: providerName, Raw: raw}
	if di, ok := raw["dirinfo"].(map[string]any); ok {
		out.ID = numToStr(di, "id")
		out.Title = strFromMap(di, "title")
		out.Cover = strFromMap(di, "picurl")
		if out.Cover == "" {
			out.Cover = strFromMap(di, "logo")
		}
		out.Description = strFromMap(di, "desc")
		if creator, ok := di["creator"].(map[string]any); ok {
			out.Creator.ID = numToStr(creator, "musicid")
			out.Creator.Nickname = strFromMap(creator, "nick")
			out.Creator.Avatar = strFromMap(creator, "headurl")
		}
		out.PlayCount = int64(intFromMap(di, "listennum"))
	}
	// total_song_num 在响应顶层 (非 dirinfo 里), 优先级最高
	if v, ok := raw["total_song_num"].(float64); ok {
		out.SongCount = int64(v)
	}
	if v, ok := raw["hasmore"].(float64); ok {
		out.HasMore = v != 0
	}
	if songs, ok := raw["songlist"].([]any); ok {
		for _, s := range songs {
			if m, ok := s.(map[string]any); ok {
				out.Songs = append(out.Songs, *adaptSongQQMusic(providerName, m))
			}
		}
	}
	return out
}

func adaptPlaylistNetease(raw map[string]any) *Playlist {
	out := &Playlist{ProviderName: "netease", Raw: raw}
	pl := raw
	if p, ok := raw["playlist"].(map[string]any); ok {
		pl = p
	}
	out.ID = numToStr(pl, "id")
	out.Title = strFromMap(pl, "name")
	out.Cover = strFromMap(pl, "coverImgUrl")
	out.Description = strFromMap(pl, "description")
	if c, ok := pl["creator"].(map[string]any); ok {
		out.Creator.ID = numToStr(c, "userId")
		out.Creator.Nickname = strFromMap(c, "nickname")
		out.Creator.Avatar = strFromMap(c, "avatarUrl")
	}
	out.PlayCount = int64(intFromMap(pl, "playCount"))
	out.SongCount = int64(intFromMap(pl, "trackCount"))
	if tracks, ok := pl["tracks"].([]any); ok {
		for _, t := range tracks {
			if m, ok := t.(map[string]any); ok {
				out.Songs = append(out.Songs, *adaptSongNetease(m))
			}
		}
	}
	return out
}

// adaptSongURLNetease 适配网易云歌曲 URL。
// 网易云返回: {id, url, br, size, type, level, fee, ...}
func adaptSongURLNetease(quality string, raw map[string]any) *SongURL {
	out := &SongURL{Quality: quality}
	out.MID = numToStr(raw, "id")
	out.URL = strFromMap(raw, "url")
	if sz, ok := raw["size"].(float64); ok {
		out.Size = int64(sz)
	}
	// type 字段标识实际格式（网易云返回 "flac"/"mp3" 等）
	if t := strFromMap(raw, "type"); t != "" {
		out.Quality = t
	}
	// level 字段是实际品质级别
	if lvl := strFromMap(raw, "level"); lvl != "" {
		out.Quality = lvl
	}
	// fee: 1=VIP
	fee := intFromMap(raw, "fee")
	freeTrialInfo := raw["freeTrialInfo"]
	if out.URL == "" {
		if fee == 1 {
			out.Subcode = 1
		} else if freeTrialInfo != nil {
			out.Subcode = 1
		}
	}
	out.Expires = 1200
	return out
}
