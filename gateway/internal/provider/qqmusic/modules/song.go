// Package modules 是 QQ 音乐业务模块的 Go 移植入口。
//
// 设计原则：每个模块导出一组以 *qqmusic.Client 为第一参数的纯函数；
// 不再使用 Python 中的 SongApi 类挂载方式，便于号池调度时按调用粒度切换凭据。
package modules

import (
	"context"
	"errors"
	"fmt"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// QuerySongResponse 是 music.trackInfo.UniformRuleCtrl/CgiGetTrackInfo 的简化响应。
//
// 仅保留 walking-skeleton 必需字段，便于 e2e demo；M9 时按 Python
// qqmusic_api.models.song.QuerySongResponse 结构补齐。
type QuerySongResponse struct {
	Tracks []map[string]any `json:"tracks"`
	Raw    map[string]any   `json:"-"` // 原始 data 字段（便于调试）
}

// QuerySongByMID 等价 Python `SongApi.query_song(mids)`：按 mid 列表批量查询。
func QuerySongByMID(ctx context.Context, c *qqmusic.Client, mids []string) (*QuerySongResponse, error) {
	if len(mids) == 0 {
		return nil, errors.New("qqmusic.song: empty mids")
	}
	param := map[string]any{
		"mids":         mids,
		"types":        zerosString(len(mids)),
		"modify_stamp": zerosString(len(mids)),
		"ctx":          0,
		"client":       1,
	}
	return doQuerySong(ctx, c, param)
}

// QuerySongByID 等价 Python `SongApi.query_song(ids)`。
func QuerySongByID(ctx context.Context, c *qqmusic.Client, ids []int64) (*QuerySongResponse, error) {
	if len(ids) == 0 {
		return nil, errors.New("qqmusic.song: empty ids")
	}
	param := map[string]any{
		"ids":          ids,
		"types":        zerosString(len(ids)),
		"modify_stamp": zerosString(len(ids)),
		"ctx":          0,
		"client":       1,
	}
	return doQuerySong(ctx, c, param)
}

func doQuerySong(ctx context.Context, c *qqmusic.Client, param map[string]any) (*QuerySongResponse, error) {
	resp, err := c.RequestMusicu(ctx, []qqmusic.RequestItem{{
		Module: "music.trackInfo.UniformRuleCtrl",
		Method: "CgiGetTrackInfo",
		Param:  param,
	}}, qqmusic.MusicuOptions{})
	if err != nil {
		return nil, err
	}
	item, ok := resp["req_0"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("qqmusic.song: missing req_0 in response: %v", resp)
	}
	if code, ok := item["code"].(float64); ok && code != 0 {
		return nil, fmt.Errorf("qqmusic.song: api error code=%v", item["code"])
	}
	data, _ := item["data"].(map[string]any)
	if data == nil {
		return nil, errors.New("qqmusic.song: missing req_0.data")
	}
	out := &QuerySongResponse{Raw: data}
	if tracks, ok := data["tracks"].([]any); ok {
		for _, t := range tracks {
			if m, ok := t.(map[string]any); ok {
				out.Tracks = append(out.Tracks, m)
			}
		}
	}
	return out, nil
}

// zerosString 返回 n 个 0 的 []int 切片（json marshal 后是 [0,0,...]）。
func zerosString(n int) []int {
	out := make([]int, n)
	return out
}

// GetSongDetail 等价 Python `SongApi.get_detail`：单首歌曲详情（走 Web platform）。
func GetSongDetail(ctx context.Context, c *qqmusic.Client, value any) (map[string]any, error) {
	param := map[string]any{}
	switch v := value.(type) {
	case int:
		param["song_id"] = v
	case int64:
		param["song_id"] = v
	default:
		param["song_mid"] = v
	}
	return callJSON(ctx, c, "music.pf_song_detail_svr", "get_song_detail_yqq", param,
		qqmusic.MusicuOptions{Platform: qqmusic.PlatformWeb})
}

// GetSongURLs 等价 Python `SongApi.get_song_urls` 简化签名：只取标准音质列表。
//
// fileType: "M500" / "M800" / "F000" / "C200" 等 4 字符前缀。
// extension: ".mp3" / ".flac" / ".m4a" 等。
// guid: 客户端 GUID（一般取 client._guid 或 utils.get_guid）。
//
// 返回 raw response，不做 vkey 解码（M9 收尾时按需补 SongURL helper）。
func GetSongURLs(ctx context.Context, c *qqmusic.Client, mids []string, fileType, extension, guid string) (map[string]any, error) {
	if len(mids) == 0 {
		return nil, ErrMissingData
	}
	filenames := make([]string, len(mids))
	for i, mid := range mids {
		// 等价 Python：f"{prefix}{mid}{mid}{ext}"
		filenames[i] = fileType + mid + mid + extension
	}
	songtype := make([]int, len(mids))
	uin := "0"
	if c.Credential() != nil && c.Credential().StrMusicID != "" {
		uin = c.Credential().StrMusicID
	}
	return callJSON(ctx, c, "music.vkey.GetVkey", "UrlGetVkey", map[string]any{
		"uin":      uin,
		"filename": filenames,
		"guid":     guid,
		"songmid":  mids,
		"songtype": songtype,
		"ctx":      0,
	}, qqmusic.MusicuOptions{})
}
