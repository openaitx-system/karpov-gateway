package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// GetUserHomepage 等价 Python `UserApi.get_homepage`：根据 euin 查用户主页。
//
// euin 即 encrypt_uin（来自 credential.encrypt_uin 或登录态自带）。
func GetUserHomepage(ctx context.Context, c *qqmusic.Client, euin string) (map[string]any, error) {
	if euin == "" {
		return nil, ErrMissingData
	}
	return callJSON(ctx, c, "music.UnifiedHomepage.UnifiedHomepageSrv", "GetHomepageHeader", map[string]any{
		"uin":              euin,
		"IsQueryTabDetail": 0,
	}, qqmusic.MusicuOptions{})
}

// GetUserSonglist 等价 Python `UserApi.get_created_songlist`：用户创建的歌单。
func GetUserSonglist(ctx context.Context, c *qqmusic.Client, euin string, num, page int) (map[string]any, error) {
	if num <= 0 {
		num = 20
	}
	if page <= 0 {
		page = 1
	}
	return callJSON(ctx, c, "music.musicasset.PlaylistBaseRead", "GetPlaylistByUin", map[string]any{
		"uin":  euin,
		"size": num,
		"page": page,
	}, qqmusic.MusicuOptions{})
}
