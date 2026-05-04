package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// GetSingerInfo 等价 Python `SingerApi.get_info` (`music.UnifiedHomepage.UnifiedHomepageSrv` / `GetHomepageHeader`).
//
// 这是歌手主页 header 的公开数据 (含 Singer 基础信息 + BaseInfo 头像/背景图 + 默认 Tab 的歌曲),
// 不需要管理员级凭证. 跟 `GetSingerDetail` (走 SingerInfoInter, Python 的 `get_desc`) 不同 ——
// 后者经常返回 code=104403 (凭证权限不足), 不适合普通号池调度. 本 gateway 对外的"歌手详情"
// 应该一律走 `GetSingerInfo`, 拿不到的字段 (例如 wiki/genre) 留空即可.
func GetSingerInfo(ctx context.Context, c *qqmusic.Client, mid string) (map[string]any, error) {
	if mid == "" {
		return nil, ErrMissingData
	}
	return callJSON(ctx, c, "music.UnifiedHomepage.UnifiedHomepageSrv", "GetHomepageHeader", map[string]any{
		"SingerMid": mid,
	}, qqmusic.MusicuOptions{Platform: qqmusic.PlatformAndroid})
}

// GetSingerDetail 等价 Python `SingerApi.get_desc` (`music.musichallSinger.SingerInfoInter` / `GetSingerDetail`).
//
// 注意: 此接口经常需要管理员凭证, 普通号池号上去会 104403; 仅在确认凭证有相应 scope 时使用.
// 对外 "/v1/qqmusic/artists/:id" 端点请改走 `GetSingerInfo`.
func GetSingerDetail(ctx context.Context, c *qqmusic.Client, mids []string) (map[string]any, error) {
	if len(mids) == 0 {
		return nil, ErrMissingData
	}
	return callJSON(ctx, c, "music.musichallSinger.SingerInfoInter", "GetSingerDetail", map[string]any{
		"singer_mids": mids,
		"groups":      1,
		"wikis":       1,
	}, qqmusic.MusicuOptions{})
}

// GetSingerSongList 等价 Python `SingerApi.get_song`：分页歌手歌曲。
func GetSingerSongList(ctx context.Context, c *qqmusic.Client, mid string, num, page int) (map[string]any, error) {
	if num <= 0 {
		num = 10
	}
	if page <= 0 {
		page = 1
	}
	return callJSON(ctx, c, "musichall.song_list_server", "GetSingerSongList", map[string]any{
		"singerMid": mid,
		"order":     1,
		"number":    num,
		"begin":     (page - 1) * num,
	}, qqmusic.MusicuOptions{})
}

// GetSingerAlbumList 等价 Python `SingerApi.get_album`。
func GetSingerAlbumList(ctx context.Context, c *qqmusic.Client, mid string, num, page int) (map[string]any, error) {
	if num <= 0 {
		num = 10
	}
	if page <= 0 {
		page = 1
	}
	return callJSON(ctx, c, "music.musichallAlbum.AlbumListServer", "GetAlbumList", map[string]any{
		"singerMid": mid,
		"order":     1,
		"number":    num,
		"begin":     (page - 1) * num,
	}, qqmusic.MusicuOptions{})
}
