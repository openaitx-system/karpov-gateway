package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// GetMVDetail 等价 Python `MvApi.get_detail`：批量获取 MV 元数据。
func GetMVDetail(ctx context.Context, c *qqmusic.Client, vids []string) (map[string]any, error) {
	if len(vids) == 0 {
		return nil, ErrMissingData
	}
	return callJSON(ctx, c, "video.VideoDataServer", "get_video_info_batch", map[string]any{
		"vidlist": vids,
		"required": []string{
			"vid", "type", "sid", "cover_pic", "duration", "singers",
			"video_switch", "msg", "name", "desc", "playcnt", "pubdate",
			"isfav", "fileid", "pic_path", "tag",
		},
	}, qqmusic.MusicuOptions{})
}

// GetMVURLs 等价 Python `MvApi.get_url`：批量取 MV 播放链接。
func GetMVURLs(ctx context.Context, c *qqmusic.Client, vids []string) (map[string]any, error) {
	if len(vids) == 0 {
		return nil, ErrMissingData
	}
	return callJSON(ctx, c, "music.stream.MvUrlProxy", "GetMvUrls", map[string]any{
		"vids":         vids,
		"request_type": 10003,
		"addrtype":     3,
		"format":       264,
	}, qqmusic.MusicuOptions{})
}
