package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// GetAlbumDetail 等价 Python `AlbumApi.get_detail`。
//
// value 为整数走 albumId，字符串走 albumMId（与 Python 一致）。
func GetAlbumDetail(ctx context.Context, c *qqmusic.Client, value any) (map[string]any, error) {
	param := map[string]any{}
	switch v := value.(type) {
	case int:
		param["albumId"] = v
	case int64:
		param["albumId"] = v
	default:
		param["albumMId"] = v
	}
	return callJSON(ctx, c, "music.musichallAlbum.AlbumInfoServer", "GetAlbumDetail", param, qqmusic.MusicuOptions{})
}

// GetAlbumSongList 等价 Python `AlbumApi.get_song`。
//
// num 与 page 默认 10/1（调用方传 <=0 时按默认）；分页用 begin = num*(page-1)。
func GetAlbumSongList(ctx context.Context, c *qqmusic.Client, value any, num, page int) (map[string]any, error) {
	if num <= 0 {
		num = 10
	}
	if page <= 0 {
		page = 1
	}
	param := map[string]any{
		"begin": num * (page - 1),
		"num":   num,
	}
	switch v := value.(type) {
	case int:
		param["albumId"] = v
	case int64:
		param["albumId"] = v
	default:
		param["albumMid"] = v
	}
	return callJSON(ctx, c, "music.musichallAlbum.AlbumSongList", "GetAlbumSongList", param, qqmusic.MusicuOptions{})
}
