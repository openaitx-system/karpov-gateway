package modules

import (
	"context"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// GetRecommendHomepage 等价 Python `RecommendApi.get_homepage`：首页推荐流。
func GetRecommendHomepage(ctx context.Context, c *qqmusic.Client) (map[string]any, error) {
	return callJSON(ctx, c, "music.musicHall.MusicHallPlatform", "GetRecommend", map[string]any{}, qqmusic.MusicuOptions{})
}
