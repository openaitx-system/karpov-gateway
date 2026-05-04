package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// GetTopAll 等价 Python `TopApi.get_all`：返回所有榜单分类。
func GetTopAll(ctx context.Context, c *qqmusic.Client) (map[string]any, error) {
	return callJSON(ctx, c, "music.musicToplist.Toplist", "GetAll", map[string]any{}, qqmusic.MusicuOptions{})
}

// GetTopDetail 等价 Python `TopApi.get_detail`：单榜详情。
//
// topid: 榜单 ID；num/page 用于歌曲翻页；tag=true 时保留 bool（与 Python preserve_bool=tag 对齐）。
func GetTopDetail(ctx context.Context, c *qqmusic.Client, topid int, num, page int, tag bool) (map[string]any, error) {
	if num <= 0 {
		num = 20
	}
	if page <= 0 {
		page = 1
	}
	return callJSON(ctx, c, "music.musicToplist.Toplist", "GetDetail", map[string]any{
		"topId":  topid,
		"offset": (page - 1) * num,
		"num":    num,
		"period": "",
	}, qqmusic.MusicuOptions{PreserveBool: tag})
}
