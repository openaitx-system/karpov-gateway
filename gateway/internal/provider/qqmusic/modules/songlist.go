package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// GetSonglistDetail 等价 Python `SonglistApi.get_detail`。
//
// disstid: 歌单 ID；num/page 用于歌曲翻页（默认 10/1）。
func GetSonglistDetail(ctx context.Context, c *qqmusic.Client, disstid int64, num, page int) (map[string]any, error) {
	if num <= 0 {
		num = 10
	}
	if page <= 0 {
		page = 1
	}
	return callJSON(ctx, c, "music.srfDissInfo.DissInfo", "CgiGetDiss", map[string]any{
		"disstid":          disstid,
		"dirid":            0,
		"tag":              1,
		"userinfo":         1,
		"orderlist":        1,
		"song_begin":       num * (page - 1),
		"song_num":         num,
		"enc_host_uin":     "",
		"onlysonglist":     0,
		"new_format":       1,
		"need_album_info":  1,
		"need_trans_info":  1,
		"need_lyric":       0,
		"need_artist_info": 1,
	}, qqmusic.MusicuOptions{})
}
