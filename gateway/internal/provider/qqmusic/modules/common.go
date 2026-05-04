package modules

import (
	"context"
	"errors"
	"fmt"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// ErrMissingData 表示 musicu.fcg 响应缺少 req_0.data。
var ErrMissingData = errors.New("qqmusic: missing req_0.data")

// extractReq0Data 通用助手：从 RequestMusicu 输出抽取 req_0.data；
// 同时检查 req_0.code（与 Python execute 行为对齐）。
func extractReq0Data(resp map[string]any) (map[string]any, error) {
	item, ok := resp["req_0"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("qqmusic: missing req_0: %v", resp)
	}
	if code, ok := item["code"].(float64); ok && code != 0 {
		return nil, fmt.Errorf("qqmusic: api error code=%v subcode=%v", item["code"], item["subcode"])
	}
	data, _ := item["data"].(map[string]any)
	if data == nil {
		return nil, ErrMissingData
	}
	return data, nil
}

// queryCommon 等价 Python `_build_query_common_params`：返回 {ct, cv}。
func queryCommon(c *qqmusic.Client) map[string]any {
	prof := c.Policy().GetProfile(c.Platform())
	return map[string]any{"ct": prof.CT, "cv": prof.CV}
}

// callJSON 是单 RequestItem 的便捷封装。
func callJSON(ctx context.Context, c *qqmusic.Client, module, method string, param map[string]any, opts qqmusic.MusicuOptions) (map[string]any, error) {
	resp, err := c.RequestMusicu(ctx, []qqmusic.RequestItem{{
		Module: module, Method: method, Param: param,
	}}, opts)
	if err != nil {
		return nil, err
	}
	return extractReq0Data(resp)
}

// mergeMap 把 src 写入 dst（in-place）。
func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}
