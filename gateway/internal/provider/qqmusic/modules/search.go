package modules

import (
	"context"
	"errors"
	"fmt"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/util"
)

// SearchType 等价 Python qqmusic_api.modules.search.SearchType（核心子集）。
type SearchType int

const (
	SearchTypeSong       SearchType = 0
	SearchTypeSinger     SearchType = 1
	SearchTypeAlbum      SearchType = 2
	SearchTypeSonglist   SearchType = 3
	SearchTypeMV         SearchType = 4
	SearchTypeLyric      SearchType = 7
	SearchTypeUser       SearchType = 8
	SearchTypeCategory   SearchType = 9
	SearchTypeAvatar     SearchType = 12
	SearchTypeAudioAlbum SearchType = 15
	SearchTypeAudio      SearchType = 18
)

// bodyKeyFor 给一个 SearchType 返回 QQ Music API response 里
// `data.body.<key>` 对应的字段名。与 Python qqmusic_api.models.search.SearchByTypeResponse
// 的 jsonpath 严格对齐。返回空字符串表示该 type 没有专属字段（走兜底逻辑）。
func bodyKeyFor(t SearchType) string {
	switch t {
	case SearchTypeSong, SearchTypeLyric:
		// Python SearchByTypeResponse.song: "单曲、歌词或节目类型下的结果列表" → item_song
		return "item_song"
	case SearchTypeSinger:
		return "singer"
	case SearchTypeAlbum:
		return "item_album"
	case SearchTypeSonglist:
		return "item_songlist"
	case SearchTypeMV:
		return "item_mv"
	case SearchTypeUser:
		return "item_user"
	case SearchTypeAudioAlbum, SearchTypeAudio:
		return "item_audio"
	default:
		return ""
	}
}

// SearchByTypeOptions 控制搜索请求。
type SearchByTypeOptions struct {
	Num       int        // 每页数量；<=0 时取 10
	Page      int        // 页码；<=0 时取 1
	SearchID  string     // 搜索会话 ID；为空时自动生成
	Highlight bool       // 是否高亮关键词
	Type      SearchType // 搜索类型；零值=Song
}

// SearchResponse 是 SearchByType 简化响应；M9 时按需补字段。
type SearchResponse struct {
	Total   int64            `json:"total"`
	HasMore bool             `json:"has_more"`
	List    []map[string]any `json:"list"`
	Raw     map[string]any   `json:"-"`
}

// SearchByType 等价 Python `SearchApi.search_by_type`。
//
// musicu.fcg 路径：module=music.search.SearchCgiService method=DoSearchForQQMusicMobile
// 强制 platform=Android（与 Python 一致：Search 接口仅在 Android comm 下稳定）。
func SearchByType(ctx context.Context, c *qqmusic.Client, keyword string, opts SearchByTypeOptions) (*SearchResponse, error) {
	if keyword == "" {
		return nil, errors.New("qqmusic.search: empty keyword")
	}
	if opts.Num <= 0 {
		opts.Num = 10
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	sid := opts.SearchID
	if sid == "" {
		sid = util.SearchID()
	}

	param := map[string]any{
		"searchid":     sid,
		"query":        keyword,
		"search_type":  int(opts.Type),
		"num_per_page": opts.Num,
		"page_num":     opts.Page,
		"highlight":    opts.Highlight,
		"grp":          true,
	}

	resp, err := c.RequestMusicu(ctx, []qqmusic.RequestItem{{
		Module: "music.search.SearchCgiService",
		Method: "DoSearchForQQMusicMobile",
		Param:  param,
	}}, qqmusic.MusicuOptions{Platform: qqmusic.PlatformAndroid})
	if err != nil {
		return nil, err
	}
	item, ok := resp["req_0"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("qqmusic.search: missing req_0: %v", resp)
	}
	if code, ok := item["code"].(float64); ok && code != 0 {
		return nil, fmt.Errorf("qqmusic.search: api error code=%v", item["code"])
	}
	data, _ := item["data"].(map[string]any)
	if data == nil {
		return nil, errors.New("qqmusic.search: missing data")
	}
	out := &SearchResponse{Raw: data}

	// Meta 字段位于 data.meta.{sum,estimate_sum,nextpage,searchid,perpage}, 不在 data 顶层.
	// (Python jsonpath: $.meta.sum / $.meta.estimate_sum / $.meta.nextpage)
	// 实测 meta.sum 经常为 0 (即便有结果), estimate_sum 才是真实总数; 因此优先选 estimate_sum.
	if meta, ok := data["meta"].(map[string]any); ok {
		if v, ok := meta["estimate_sum"].(float64); ok && v > 0 {
			out.Total = int64(v)
		}
		if out.Total == 0 {
			if v, ok := meta["sum"].(float64); ok {
				out.Total = int64(v)
			}
		}
		if v, ok := meta["nextpage"].(float64); ok {
			out.HasMore = v != -1
		}
	} else {
		// 极个别 cgi 旧路径会把 sum 直接放 data 顶层, 兜底
		if v, ok := data["total_num"].(float64); ok {
			out.Total = int64(v)
		}
		if v, ok := data["nextpage"].(float64); ok {
			out.HasMore = v != -1
		}
	}

	// body 下按 SearchType 精确取字段, 字段值直接是 list (非 dict.list 结构).
	// Python: body.item_song / body.singer / body.item_album / body.item_songlist / ...
	body, _ := data["body"].(map[string]any)
	if body == nil {
		return out, nil
	}

	pickList := func(key string) []any {
		if key == "" {
			return nil
		}
		if arr, ok := body[key].([]any); ok {
			return arr
		}
		// 个别字段为兼容旧版可能仍是 {list: [...]} 结构
		if section, ok := body[key].(map[string]any); ok {
			if arr, ok := section["list"].([]any); ok {
				return arr
			}
		}
		return nil
	}

	items := pickList(bodyKeyFor(opts.Type))
	if items == nil {
		// 兜底: 未识别的 search_type 时, 选 body 下第一个 array 类型字段.
		// 这里要避开 `direct_result` / `meta` 这种 dict 结构.
		for _, v := range body {
			if arr, ok := v.([]any); ok && len(arr) > 0 {
				items = arr
				break
			}
		}
	}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out.List = append(out.List, m)
		}
	}
	return out, nil
}
