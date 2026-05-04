package util

// BoolToInt 递归把数据结构里所有 bool 转换成 0/1 整数；其他类型原样返回。
//
// 等价 Python qqmusic_api.utils.common.bool_to_int。设计意图：QQ 音乐
// musicu.fcg 部分接口校验 param 中 bool 字段会失败，必须先归一为 0/1。
//
// 支持的容器：map[string]any、map[int]any、[]any。其他切片/复合类型按
// 需求补充。
func BoolToInt(v any) any {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case map[string]any:
		hasComplex := false
		for _, vv := range x {
			if _, ok := vv.(bool); ok {
				hasComplex = true
				break
			}
			if _, ok := vv.(map[string]any); ok {
				hasComplex = true
				break
			}
			if _, ok := vv.([]any); ok {
				hasComplex = true
				break
			}
			if _, ok := vv.(map[int]any); ok {
				hasComplex = true
				break
			}
		}
		if !hasComplex {
			return x
		}
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = BoolToInt(vv)
		}
		return out
	case map[int]any:
		out := make(map[int]any, len(x))
		for k, vv := range x {
			out[k] = BoolToInt(vv)
		}
		return out
	case []any:
		hasComplex := false
		for _, vv := range x {
			if _, ok := vv.(bool); ok {
				hasComplex = true
				break
			}
			if _, ok := vv.(map[string]any); ok {
				hasComplex = true
				break
			}
			if _, ok := vv.([]any); ok {
				hasComplex = true
				break
			}
		}
		if !hasComplex {
			return x
		}
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = BoolToInt(vv)
		}
		return out
	default:
		return v
	}
}
