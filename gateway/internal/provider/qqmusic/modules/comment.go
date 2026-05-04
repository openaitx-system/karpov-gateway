package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// CommentBizType 等价 Python `CommentApi.BizType`：评论业务类别。
type CommentBizType int

const (
	CommentBizSong     CommentBizType = 1
	CommentBizAlbum    CommentBizType = 8
	CommentBizSonglist CommentBizType = 5
	CommentBizMV       CommentBizType = 4
)

// GetHotComments 等价 Python `CommentApi.get_hot_comments`：热门评论。
//
// bizID: 资源数字 ID（非 MID）；num/page 翻页（默认 15/1，page 为 1-based）。
func GetHotComments(ctx context.Context, c *qqmusic.Client, biz CommentBizType, bizID string, num, page int) (map[string]any, error) {
	if num <= 0 {
		num = 15
	}
	if page <= 0 {
		page = 1
	}
	return callJSON(ctx, c, "music.globalComment.CommentRead", "GetHotCommentList", map[string]any{
		"BizType":      int(biz),
		"BizId":        bizID,
		"PageNum":      page - 1,
		"PageSize":     num,
		"HotType":      1,
		"WithAirborne": 0,
		"PicEnable":    1,
	}, qqmusic.MusicuOptions{})
}
