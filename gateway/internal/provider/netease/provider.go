package netease

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

const ProviderName = "netease"

// Provider 实现 provider.MusicProvider。
type Provider struct {
	base *Client
}

func NewProvider(base *Client) *Provider {
	if base == nil {
		base = NewClient(ClientOptions{})
	}
	return &Provider{base: base}
}

func (p *Provider) Name() string { return ProviderName }

func (p *Provider) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapGetSong,
		provider.CapSearchSongs,
		provider.CapGetSongURL,
		provider.CapGetLyric,
		provider.CapGetAlbum,
		provider.CapGetSinger,
		provider.CapGetSongList,
		provider.CapGetTopList,
		provider.CapGetRecommend,
		provider.CapGetComments,
		provider.CapGetMV,
		provider.CapGetMVURL,
	}
}

func (p *Provider) withLease(lease *provider.CredentialLease) *Client {
	if lease == nil || len(lease.Payload) == 0 {
		return p.base
	}
	// payload 格式：{"cookie":"MUSIC_U=xxx;..."}
	var cred struct {
		Cookie string `json:"cookie"`
	}
	if err := json.Unmarshal(lease.Payload, &cred); err == nil && cred.Cookie != "" {
		return p.base.WithCookie(cred.Cookie)
	}
	// 兼容：payload 直接是 cookie 字符串
	cookie := strings.TrimSpace(string(lease.Payload))
	if strings.Contains(cookie, "MUSIC_U") {
		return p.base.WithCookie(cookie)
	}
	return p.base
}

func (p *Provider) GetSong(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c := p.withLease(lease)
	resp, err := c.GetSongDetail(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	songs, ok := resp["songs"].([]any)
	if !ok || len(songs) == 0 {
		return nil, fmt.Errorf("netease: song not found: %s", id)
	}
	song, _ := songs[0].(map[string]any)
	return song, nil
}

func (p *Provider) SearchSongs(ctx context.Context, lease *provider.CredentialLease, query string, page, size int) (map[string]any, error) {
	c := p.withLease(lease)
	offset := (page - 1) * size
	if offset < 0 {
		offset = 0
	}
	return c.Search(ctx, query, 1, size, offset)
}

func (p *Provider) GetSongURL(ctx context.Context, lease *provider.CredentialLease, id string, quality string) (map[string]any, error) {
	c := p.withLease(lease)
	level := mapQuality(quality)
	resp, err := c.GetSongURL(ctx, []string{id}, level)
	if err != nil {
		return nil, err
	}
	data, _ := resp["data"].([]any)
	if len(data) == 0 {
		return nil, fmt.Errorf("netease: no url data for song %s", id)
	}
	urlData, _ := data[0].(map[string]any)
	return urlData, nil
}

func (p *Provider) GetLyric(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c := p.withLease(lease)
	resp, err := c.GetLyric(ctx, id)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if lrc, ok := resp["lrc"].(map[string]any); ok {
		result["lyric"], _ = lrc["lyric"].(string)
	}
	if tlyric, ok := resp["tlyric"].(map[string]any); ok {
		result["trans"], _ = tlyric["lyric"].(string)
	}
	if romalrc, ok := resp["romalrc"].(map[string]any); ok {
		result["roma"], _ = romalrc["lyric"].(string)
	}
	if yrc, ok := resp["yrc"].(map[string]any); ok {
		result["qrc"], _ = yrc["lyric"].(string) // 逐字歌词
	}
	return result, nil
}

func (p *Provider) GetAlbum(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c := p.withLease(lease)
	return c.GetAlbum(ctx, id)
}

// GetSingerDetail 走 `/api/artist/head/info/get` (与 api-enhanced-main `artist_detail.js` 对齐).
// 返回 `{data: {artist: {id, name, cover, briefDesc, transName, alias, identity, ...}}}`,
// 比 `/api/v1/artist/{id}` 拿到的 hotSongs+artist 摘要更完整, 用作 "歌手详情" 端点更合适.
func (p *Provider) GetSingerDetail(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c := p.withLease(lease)
	return c.GetArtistDetail(ctx, id)
}

func (p *Provider) GetSongList(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c := p.withLease(lease)
	return c.GetPlaylistDetail(ctx, id)
}

func (p *Provider) GetMVDetail(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c := p.withLease(lease)
	return c.GetMVDetail(ctx, id)
}

func (p *Provider) GetMVURL(ctx context.Context, lease *provider.CredentialLease, id string) (map[string]any, error) {
	c := p.withLease(lease)
	return c.GetMVURL(ctx, id, 1080)
}

func (p *Provider) HealthCheck(ctx context.Context, lease *provider.CredentialLease) error {
	c := p.withLease(lease)
	_, err := c.Search(ctx, "test", 1, 1, 0)
	return err
}

func (p *Provider) TestCapability(ctx context.Context, lease *provider.CredentialLease, cap provider.Capability) error {
	c := p.withLease(lease)
	var err error
	switch cap {
	case provider.CapGetSong:
		_, err = c.GetSongDetail(ctx, []string{"347230"})
	case provider.CapSearchSongs:
		_, err = c.Search(ctx, "周杰伦", 1, 1, 0)
	case provider.CapGetSongURL:
		_, err = c.GetSongURL(ctx, []string{"347230"}, "standard")
	case provider.CapGetLyric:
		_, err = c.GetLyric(ctx, "347230")
	case provider.CapGetAlbum:
		_, err = c.GetAlbum(ctx, "32311")
	case provider.CapGetSinger:
		_, err = c.GetArtist(ctx, "6452")
	case provider.CapGetSongList:
		_, err = c.GetPlaylistDetail(ctx, "24381616")
	case provider.CapGetTopList:
		_, err = c.GetToplist(ctx)
	case provider.CapGetRecommend:
		_, err = c.GetPersonalized(ctx, 1)
	case provider.CapGetComments:
		_, err = c.GetCommentMusic(ctx, "347230", 1, 0)
	case provider.CapGetMV:
		_, err = c.GetMVDetail(ctx, "5436712")
	case provider.CapGetMVURL:
		_, err = c.GetMVURL(ctx, "5436712", 480)
	default:
		_, err = c.Search(ctx, "test", 1, 1, 0)
	}
	return err
}

// Register 注册 netease provider 到 registry。
func Register(reg *provider.Registry, base *Client) error {
	return reg.Register(NewProvider(base))
}

func mapQuality(q string) string {
	switch strings.ToUpper(q) {
	case "MP3_128", "STANDARD":
		return "standard"
	case "MP3_320", "EXHIGH":
		return "exhigh"
	case "FLAC", "LOSSLESS":
		return "lossless"
	case "MASTER", "HIRES":
		return "hires"
	case "DOLBY", "JYEFFECT":
		return "jyeffect"
	case "ATMOS", "SKY":
		return "sky"
	case "JYMASTER":
		return "jymaster"
	default:
		return "exhigh"
	}
}
