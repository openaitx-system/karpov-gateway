package util

// Hash33 实现 DJB-style 33 哈希，对每个 Unicode 码点（rune）按
//
//	h = (h<<5) + h + ord(c)
//
// 累加，最后用 0x7FFFFFFF 截断到 31 位正整数。
//
// 等价于 Python qqmusic_api.utils.common.hash33：迭代用 ord(c)，因此
// 对中/英文字符串语义都按 rune 处理（不是按 byte）。返回 int64 以
// 与 fixture 中的 Python int 范围对齐（实际值 ≤ 2^31-1）。
func Hash33(s string, h int64) int64 {
	for _, r := range s {
		h = (h << 5) + h + int64(r)
	}
	return h & 0x7FFFFFFF
}
